package identity

import (
	"errors"
	"io"

	"io/ioutil"

	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/network"
	"github.com/dedis/cothority/sda"
	"github.com/dedis/cothority/services/skipchain"
	"github.com/dedis/crypto/eddsa"
)

/*
This is the external API to access the identity-service. It shows the methods
used to create a new identity-skipchain, propose new configurations and how
to vote on these configurations.
*/

func init() {
	for _, s := range []interface{}{
		// Structures
		&Device{},
		&Identity{},
		&Config{},
		&Storage{},
		&Service{},
		// API messages
		&CreateIdentity{},
		&CreateIdentityReply{},
		&ConfigUpdate{},
		&ConfigUpdateReply{},
		&ProposeSend{},
		&ProposeUpdate{},
		&ProposeUpdateReply{},
		&ProposeVote{},
		&ProposeVoteReply{},
		&GetUpdateChain{},
		&GetUpdateChainReply{},
		// Internal messages
		&PropagateIdentity{},
		&UpdateSkipBlock{},
	} {
		network.RegisterPacketType(s)
	}
}

// Identity structure holds the data necessary for a client/device to use the
// identity-service. Each identity-skipchain is tied to a roster that is defined
// in 'Cothority'.
type Identity struct {
	// Client is included for easy `Send`-methods.
	*sda.Client
	// The eddsa-compatible key - cannot take suite.Private as the
	// eddsa-signaure needs access to the seed and the prefix of the
	// private key.
	EdDSA *eddsa.EdDSA
	// ID of the skipchain this device is tied to.
	ID ID
	// Config is the actual, valid configuration of the identity-skipchain.
	Config *Config
	// Proposed is the new configuration that has not been validated by a
	// threshold of devices.
	Proposed *Config
	// DeviceName must be unique in the identity-skipchain.
	DeviceName string
	// Cothority is the roster responsible for the identity-skipchain. It
	// might change in the case of a roster-update.
	Cothority *sda.Roster
}

// NewIdentity starts a new identity that can contain multiple managers with
// different accounts
func NewIdentity(cothority *sda.Roster, threshold int, owner string) *Identity {
	client := sda.NewClient(ServiceName)
	ed := eddsa.NewEdDSA(nil)
	return &Identity{
		Client:     client,
		Config:     NewConfig(threshold, ed.Public, owner),
		DeviceName: owner,
		Cothority:  cothority,
		EdDSA:      ed,
	}
}

// NewIdentityFromCothority searches for a given cothority
func NewIdentityFromCothority(el *sda.Roster, id ID) (*Identity, error) {
	iden := &Identity{
		Client:    sda.NewClient(ServiceName),
		Cothority: el,
		ID:        id,
	}
	err := iden.ConfigUpdate()
	if err != nil {
		return nil, err
	}
	return iden, nil
}

// NewIdentityFromStream reads the configuration of that client from
// any stream
func NewIdentityFromStream(in io.Reader) (*Identity, error) {
	data, err := ioutil.ReadAll(in)
	if err != nil {
		return nil, err
	}
	_, id, err := network.UnmarshalRegistered(data)
	if err != nil {
		return nil, err
	}
	return id.(*Identity), nil
}

// SaveToStream stores the configuration of the client to a stream
func (i *Identity) SaveToStream(out io.Writer) error {
	data, err := network.MarshalRegisteredType(i)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

// GetProposed returns the Propose-field or a copy of the config if
// the Propose-field is nil
func (i *Identity) GetProposed() *Config {
	if i.Proposed != nil {
		return i.Proposed
	}
	return i.Config.Copy()
}

// AttachToIdentity proposes to attach it to an existing Identity
func (i *Identity) AttachToIdentity(ID ID) error {
	i.ID = ID
	err := i.ConfigUpdate()
	if err != nil {
		return err
	}
	if _, exists := i.Config.Device[i.DeviceName]; exists {
		return errors.New("Adding with an existing account-name")
	}
	confPropose := i.Config.Copy()
	confPropose.Device[i.DeviceName] = &Device{i.EdDSA.Public}
	err = i.ProposeSend(confPropose)
	if err != nil {
		return err
	}
	return nil
}

// CreateIdentity asks the identityService to create a new Identity
func (i *Identity) CreateIdentity() error {
	msg, err := i.Send(i.Cothority.RandomServerIdentity(), &CreateIdentity{i.Config, i.Cothority})
	if err != nil {
		return err
	}
	air := msg.Msg.(CreateIdentityReply)
	i.ID = ID(air.Data.Hash)

	return nil
}

// ProposeSend sends the new proposition of this identity
// ProposeVote
func (i *Identity) ProposeSend(il *Config) error {
	_, err := i.Send(i.Cothority.RandomServerIdentity(), &ProposeSend{i.ID, il})
	i.Proposed = il
	return err
}

// ProposeUpdate verifies if there is a new configuration awaiting that
// needs approval from clients
func (i *Identity) ProposeUpdate() error {
	msg, err := i.Send(i.Cothority.RandomServerIdentity(), &ProposeUpdate{
		ID: i.ID,
	})
	if err != nil {
		return err
	}
	cnc := msg.Msg.(ProposeUpdateReply)
	i.Proposed = cnc.Propose
	return nil
}

// ProposeVote calls the 'accept'-vote on the current propose-configuration
func (i *Identity) ProposeVote(accept bool) error {
	if i.Proposed == nil {
		return errors.New("No proposed config")
	}
	log.Lvlf3("Voting %t on %s", accept, i.Proposed.Device)
	if !accept {
		return nil
	}
	hash, err := i.Proposed.Hash()
	if err != nil {
		return err
	}
	log.Print(i.EdDSA)
	sig, err := i.EdDSA.Sign(hash)
	if err != nil {
		return err
	}
	msg, err := i.Send(i.Cothority.RandomServerIdentity(), &ProposeVote{
		ID:        i.ID,
		Signer:    i.DeviceName,
		Signature: sig,
	})
	err = sda.ErrMsg(msg, err)
	if err != nil {
		return err
	}
	_, ok := msg.Msg.(ProposeVoteReply)
	if ok {
		log.Lvl2("Threshold reached and signed")
		i.Config = i.Proposed
		i.Proposed = nil
	} else {
		log.Lvl2("Threshold not reached")
	}
	return nil
}

// GetUpdateChain returns the individual skipblocks for verification by
// the client
func (i *Identity) GetUpdateChain(id ID) ([]*skipchain.SkipBlock, error) {
	msg, err := i.Send(i.Cothority.RandomServerIdentity(),
		&skipchain.GetUpdateChain{skipchain.SkipBlockID(id)})
	if err != nil {
		return nil, err
	}
	return msg.Msg.(*skipchain.GetUpdateChainReply).Update, nil
}

// ConfigUpdate asks if there is any new config available that has already
// been approved by others and updates the local configuration
func (i *Identity) ConfigUpdate() error {
	if i.Cothority == nil || len(i.Cothority.List) == 0 {
		return errors.New("Didn't find any list in the cothority")
	}
	msg, err := i.Send(i.Cothority.RandomServerIdentity(), &ConfigUpdate{ID: i.ID})
	if err != nil {
		return err
	}
	cu := msg.Msg.(ConfigUpdateReply)
	// TODO - verify new config
	i.Config = cu.Config
	return nil
}
