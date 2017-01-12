/*
Identity is a service that allows storing of key/value pairs that belong to
a given identity that is shared between multiple devices. In order to
add/remove devices or add/remove key/value-pairs, a 'threshold' of devices
need to vote on those changes.

The key/value-pairs are stored in a personal blockchain and signed by the
cothority using forward-links, so that an external observer can check the
collective signatures and be assured that the blockchain is valid.
*/

package identity

import (
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"sync"

	"github.com/dedis/cothority/messaging"
	"github.com/dedis/cothority/skipchain"
	"github.com/dedis/onet"
	"github.com/dedis/onet/crypto"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
)

// ServiceName can be used to refer to the name of this service
const ServiceName = "Identity"

var identityService onet.ServiceID

func init() {
	identityService, _ = onet.RegisterNewService(ServiceName, newIdentityService)
	network.RegisterMessage(&StorageMap{})
	network.RegisterMessage(&Storage{})
}

// Service handles identities
type Service struct {
	*onet.ServiceProcessor
	*StorageMap
	propagateIdentity  messaging.PropagationFunc
	propagateSkipBlock messaging.PropagationFunc
	propagateConfig    messaging.PropagationFunc
	identitiesMutex    sync.Mutex
	skipchain          *skipchain.Client
	path               string
}

// StorageMap holds the map to the storages so it can be marshaled.
type StorageMap struct {
	Identities map[string]*Storage
}

// Storage stores one identity together with the skipblocks.
type Storage struct {
	sync.Mutex
	Latest   *Config
	Proposed *Config
	Votes    map[string]*crypto.SchnorrSig
	Root     *skipchain.SkipBlock
	Data     *skipchain.SkipBlock
}

/*
 * API messages
 */

// CreateIdentity will register a new SkipChain and add it to our list of
// managed identities.
func (s *Service) CreateIdentity(ai *CreateIdentity) (network.Message, onet.ClientError) {
	log.Lvlf3("%s Creating new identity with config %+v", s, ai.Config)
	ids := &Storage{
		Latest: ai.Config,
	}
	log.Lvl3("Creating Root-skipchain")
	var cerr onet.ClientError
	ids.Root, cerr = s.skipchain.CreateRoster(ai.Roster, 2, 10,
		skipchain.VerifyNone, nil)
	if cerr != nil {
		return nil, cerr
	}
	log.Lvl3("Creating Data-skipchain")
	ids.Root, ids.Data, cerr = s.skipchain.CreateData(ids.Root, 2, 10,
		skipchain.VerifyNone, ai.Config)
	if cerr != nil {
		return nil, cerr
	}

	roster := ids.Root.Roster
	replies, err := s.propagateIdentity(roster, &PropagateIdentity{ids}, propagateTimeout)
	if err != nil {
		return nil, onet.NewClientErrorCode(ErrorOnet, err.Error())
	}
	if replies != len(roster.List) {
		log.Warn("Did only get", replies, "out of", len(roster.List))
	}
	log.Lvlf2("New chain is\n%x", []byte(ids.Data.Hash))
	s.save()

	return &CreateIdentityReply{
		Root: ids.Root,
		Data: ids.Data,
	}, nil
}

// ConfigUpdate returns a new configuration update
func (s *Service) ConfigUpdate(cu *ConfigUpdate) (network.Message, onet.ClientError) {
	sid := s.getIdentityStorage(cu.ID)
	if sid == nil {
		return nil, onet.NewClientErrorCode(ErrorBlockMissing, "Didn't find Identity")
	}
	sid.Lock()
	defer sid.Unlock()
	log.Lvl3(s, "Sending config-update")
	return &ConfigUpdateReply{
		Config: sid.Latest,
	}, nil
}

// ProposeSend only stores the proposed configuration internally. Signatures
// come later.
func (s *Service) ProposeSend(p *ProposeSend) (network.Message, onet.ClientError) {
	log.Lvl2(s, "Storing new proposal")
	sid := s.getIdentityStorage(p.ID)
	if sid == nil {
		return nil, onet.NewClientErrorCode(ErrorBlockMissing, "Didn't find Identity")
	}
	roster := sid.Root.Roster
	replies, err := s.propagateConfig(roster, p, propagateTimeout)
	if err != nil {
		return nil, onet.NewClientErrorCode(ErrorOnet, err.Error())
	}
	if replies != len(roster.List) {
		log.Warn("Did only get", replies, "out of", len(roster.List))
	}
	return nil, nil
}

// ProposeUpdate returns an eventual config-proposition
func (s *Service) ProposeUpdate(cnc *ProposeUpdate) (network.Message, onet.ClientError) {
	log.Lvl3(s, "Sending proposal-update to client")
	sid := s.getIdentityStorage(cnc.ID)
	if sid == nil {
		return nil, onet.NewClientErrorCode(ErrorBlockMissing, "Didn't find Identity")
	}
	sid.Lock()
	defer sid.Unlock()
	return &ProposeUpdateReply{
		Propose: sid.Proposed,
	}, nil
}

// ProposeVote takes int account a vote for the proposed config. It also verifies
// that the voter is in the latest config.
// An empty signature signifies that the vote has been rejected.
func (s *Service) ProposeVote(v *ProposeVote) (network.Message, onet.ClientError) {
	log.Lvl2(s, "Voting on proposal")
	// First verify if the signature is legitimate
	sid := s.getIdentityStorage(v.ID)
	if sid == nil {
		return nil, onet.NewClientErrorCode(ErrorBlockMissing, "Didn't find identity")
	}

	// Putting this in a function because of the lock which needs to be held
	// over all calls that might return an error.
	cerr := func() onet.ClientError {
		sid.Lock()
		defer sid.Unlock()
		log.Lvl3("Voting on", sid.Proposed.Device)
		owner, ok := sid.Latest.Device[v.Signer]
		if !ok {
			return onet.NewClientErrorCode(ErrorAccountMissing, "Didn't find signer")
		}
		if sid.Proposed == nil {
			return onet.NewClientErrorCode(ErrorConfigMissing, "No proposed block")
		}
		hash, err := sid.Proposed.Hash()
		if err != nil {
			return onet.NewClientErrorCode(ErrorOnet, "Couldn't get hash")
		}
		if _, exists := sid.Votes[v.Signer]; exists {
			return onet.NewClientErrorCode(ErrorVoteDouble, "Already voted for that block")
		}
		log.Lvl3(v.Signer, "voted", v.Signature)
		if v.Signature != nil {
			err = crypto.VerifySchnorr(network.Suite, owner.Point, hash, *v.Signature)
			if err != nil {
				return onet.NewClientErrorCode(ErrorVoteSignature, "Wrong signature: "+err.Error())
			}
		}
		return nil
	}()
	if cerr != nil {
		return nil, cerr
	}

	// Propagate the vote
	_, err := s.propagateConfig(sid.Root.Roster, v, propagateTimeout)
	if err != nil {
		return nil, onet.NewClientErrorCode(ErrorOnet, cerr.Error())
	}
	if len(sid.Votes) >= sid.Latest.Threshold ||
		len(sid.Votes) == len(sid.Latest.Device) {
		// If we have enough signatures, make a new data-skipblock and
		// propagate it
		log.Lvl3("Having majority or all votes")

		// Making a new data-skipblock
		log.Lvl3("Sending data-block with", sid.Proposed.Device)
		reply, cerr := s.skipchain.ProposeData(sid.Root, sid.Data, sid.Proposed)
		if cerr != nil {
			return nil, cerr
		}
		_, msg, _ := network.Unmarshal(reply.Latest.Data)
		log.Lvl3("SB signed is", msg.(*Config).Device)
		usb := &UpdateSkipBlock{
			ID:     v.ID,
			Latest: reply.Latest,
		}
		_, err = s.propagateSkipBlock(sid.Root.Roster, usb, propagateTimeout)
		if err != nil {
			return nil, onet.NewClientErrorCode(ErrorOnet, cerr.Error())
		}
		s.save()
		return &ProposeVoteReply{sid.Data}, nil
	}
	return nil, nil
}

/*
 * Internal messages
 */

// propagateConfig handles propagation of all configuration-proposals in the identity-service.
func (s *Service) propagateConfigHandler(msg network.Message) {
	log.Lvlf4("Got msg %+v %v", msg, reflect.TypeOf(msg).String())
	id := ID(nil)
	switch msg.(type) {
	case *ProposeSend:
		id = msg.(*ProposeSend).ID
	case *ProposeVote:
		id = msg.(*ProposeVote).ID
	default:
		log.Errorf("Got an unidentified propagation-request: %v", msg)
		return
	}

	if id != nil {
		sid := s.getIdentityStorage(id)
		if sid == nil {
			log.Error("Didn't find entity in", s)
			return
		}
		sid.Lock()
		defer sid.Unlock()
		switch msg.(type) {
		case *ProposeSend:
			p := msg.(*ProposeSend)
			sid.Proposed = p.Config
			sid.Votes = make(map[string]*crypto.SchnorrSig)
		case *ProposeVote:
			v := msg.(*ProposeVote)
			sid.Votes[v.Signer] = v.Signature
		}
	}
}

// propagateSkipBlock saves a new skipblock to the identity
func (s *Service) propagateSkipBlockHandler(msg network.Message) {
	log.Lvlf4("Got msg %+v %v", msg, reflect.TypeOf(msg).String())
	usb, ok := msg.(*UpdateSkipBlock)
	if !ok {
		log.Error("Wrong message-type")
		return
	}
	sid := s.getIdentityStorage(usb.ID)
	if sid == nil {
		log.Error("Didn't find entity in", s)
		return
	}
	sid.Lock()
	defer sid.Unlock()
	skipblock := msg.(*UpdateSkipBlock).Latest
	_, msgLatest, err := network.Unmarshal(skipblock.Data)
	if err != nil {
		log.Error(err)
		return
	}
	al, ok := msgLatest.(*Config)
	if !ok {
		log.Error(err)
		return
	}
	sid.Data = skipblock
	sid.Latest = al
	sid.Proposed = nil
}

// propagateIdentity stores a new identity in all nodes.
func (s *Service) propagateIdentityHandler(msg network.Message) {
	log.Lvlf4("Got msg %+v %v", msg, reflect.TypeOf(msg).String())
	pi, ok := msg.(*PropagateIdentity)
	if !ok {
		log.Error("Got a wrong message for propagation")
		return
	}
	id := ID(pi.Data.Hash)
	if s.getIdentityStorage(id) != nil {
		log.Error("Couldn't store new identity")
		return
	}
	log.Lvl3("Storing identity in", s)
	s.setIdentityStorage(id, pi.Storage)
	return
}

// getIdentityStorage returns the corresponding IdentityStorage or nil
// if none was found
func (s *Service) getIdentityStorage(id ID) *Storage {
	s.identitiesMutex.Lock()
	defer s.identitiesMutex.Unlock()
	is, ok := s.Identities[string(id)]
	if !ok {
		return nil
	}
	return is
}

// setIdentityStorage saves an IdentityStorage
func (s *Service) setIdentityStorage(id ID, is *Storage) {
	s.identitiesMutex.Lock()
	defer s.identitiesMutex.Unlock()
	log.Lvlf3("%s %x %v", s.Context.ServerIdentity(), id[0:8], is.Latest.Device)
	s.Identities[string(id)] = is
}

// saves the actual identity
func (s *Service) save() {
	log.Lvl3("Saving service")
	b, err := network.Marshal(s.StorageMap)
	if err != nil {
		log.Error("Couldn't marshal service:", err)
	} else {
		err = ioutil.WriteFile(s.path+"/identity.bin", b, 0660)
		if err != nil {
			log.Error("Couldn't save file:", err)
		}
	}
}

func (s *Service) clearIdentities() {
	s.Identities = make(map[string]*Storage)
}

// Tries to load the configuration and updates if a configuration
// is found, else it returns an error.
func (s *Service) tryLoad() error {
	configFile := s.path + "/identity.bin"
	b, err := ioutil.ReadFile(configFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Error while reading %s: %s", configFile, err)
	}
	if len(b) > 0 {
		_, msg, err := network.Unmarshal(b)
		if err != nil {
			return fmt.Errorf("Couldn't unmarshal: %s", err)
		}
		log.Lvl3("Successfully loaded")
		s.StorageMap = msg.(*StorageMap)
	}
	return nil
}

func newIdentityService(c *onet.Context, path string) onet.Service {
	s := &Service{
		ServiceProcessor: onet.NewServiceProcessor(c),
		StorageMap:       &StorageMap{make(map[string]*Storage)},
		skipchain:        skipchain.NewClient(),
		path:             path,
	}
	var err error
	s.propagateIdentity, err =
		messaging.NewPropagationFunc(c, "IdentityPropagateID", s.propagateIdentityHandler)
	if err != nil {
		return nil
	}
	s.propagateSkipBlock, err =
		messaging.NewPropagationFunc(c, "IdentityPropagateSB", s.propagateSkipBlockHandler)
	if err != nil {
		return nil
	}
	s.propagateConfig, err =
		messaging.NewPropagationFunc(c, "IdentityPropagateConf", s.propagateConfigHandler)
	if err != nil {
		return nil
	}
	if err := s.tryLoad(); err != nil {
		log.Error(err)
	}
	for _, f := range []interface{}{s.ProposeSend, s.ProposeVote,
		s.CreateIdentity, s.ProposeUpdate, s.ConfigUpdate} {
		if err := s.RegisterHandler(f); err != nil {
			log.Fatal("Registration error:", err)
		}
	}
	return s
}
