package sshks

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/dedis/cothority/lib/cosi"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	"github.com/dedis/crypto/abstract"
	"github.com/dedis/crypto/config"
)

// ClientKS represents one client and holds all necessary structures
type ClientKS struct {
	// This points to the client holding this structure
	This *Client
	// Config holds all servers and clients
	Config *Config
	// NewConfig is non-nil if there is a configuration waiting
	NewConfig *Config
	// Private is our private key
	Private abstract.Secret
	// The cosi-structure holds some fields that need to be stored from
	// one invocation to another so we can still have a valid commit
	Cosi *cosi.Cosi
}

// NewClientKS creates a new private/public key pair and returns
// a ClientKS with an empty Config. It takes a public ssh-key.
func NewClientKS(sshPub string) *ClientKS {
	pair := config.NewKeyPair(network.Suite)
	cks := &ClientKS{
		This:    NewClient(pair.Public, sshPub),
		Config:  NewConfig(0),
		Private: pair.Secret,
		Cosi:    cosi.NewCosi(network.Suite, pair.Secret),
	}
	cks.Config.Clients[cks.This.Id()] = cks.This
	cks.NewConfig = cks.Config
	return cks
}

// ReadClientKS searches for the client-ks and creates a new one if it
// doesn't exist
func ReadClientKS(f string) (*ClientKS, error) {
	file := ExpandHDir(f)
	ca := NewClientKS("TestClient-")
	_, err := os.Stat(file)
	if os.IsNotExist(err) {
		return ca, nil
	}
	b, err := ioutil.ReadFile(ExpandHDir(file))
	if err != nil {
		return nil, err
	}
	_, msg, err := network.UnmarshalRegisteredType(b, network.DefaultConstructors(network.Suite))
	if err != nil {
		return nil, err
	}
	c, ok := msg.(ClientKS)
	if !ok {
		return nil, errors.New("Didn't get a ClientKS structure")
	}
	ca = &c
	ca.Cosi.SetSuite(network.Suite)
	return ca, nil
}

// Write takes a file and writes the clientKS to that file
func (ca *ClientKS) Write(file string) error {
	b, err := network.MarshalRegisteredType(ca)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(ExpandHDir(file), b, 0660)
	return err
}

// NetworkSendFirstCommit sends the config to be proposed to the other clients
func (ca *ClientKS) NetworkSendFirstCommit(s *Server) error {
	sfc := &SendFirstCommit{
		Commitment: ca.Cosi.CreateCommitment(),
	}
	dbg.Lvl3("Sending first commit")
	status, err := NetworkSend(ca.Private, s.Entity, sfc)
	return ErrMsg(status, err)
}

// NetworkSendNewConfig sends the config to be proposed to the other clients
func (ca *ClientKS) NetworkSendNewConfig(s *Server) error {
	sc := &SendNewConfig{
		Config: ca.NewConfig,
	}
	reply, err := NetworkSend(ca.Private, s.Entity, sc)
	if err != nil {
		return err
	}
	replyCh, ok := reply.Msg.(SendNewConfigRet)
	if !ok {
		return ErrMsg(reply, nil)
	}
	if replyCh.Challenge == nil {
		return errors.New("Challenge is nil")
	}
	dbg.Lvl3("Received challenge", replyCh.Challenge)
	ca.Cosi.Challenge(&cosi.Challenge{replyCh.Challenge})
	return nil
}

// NetworkGetConfig asks the server for its configuration and returns also
// an eventual configuration to be signed
func (ca *ClientKS) NetworkGetConfig(s *Server) (*Config, *Config, error) {
	resp, err := NetworkSend(ca.Private, s.Entity, &GetConfig{})
	if err != nil {
		return nil, nil, err
	}
	gcr, ok := resp.Msg.(GetConfigRet)
	if !ok {
		return nil, nil, errors.New("Didn't receive config")
	}
	return gcr.Config, gcr.NewConfig, nil
}

// NetworkRespond sends the CoSi-response and a new commit to the
// server
func (ca *ClientKS) NetworkResponse(s *Server) (int, int, error) {
	dbg.Lvl3("Asking server", s.Entity.Addresses[0], "to sign")
	cosi_new := cosi.NewCosi(network.Suite, ca.Private)
	cosiResponse, err := ca.Cosi.CreateResponse()
	dbg.Lvl3("Response is", cosiResponse.Response)
	if err != nil {
		return -1, -1, err
	}
	response := &Response{
		Response:       cosiResponse,
		NextCommitment: cosi_new.CreateCommitment(),
	}
	ca.Cosi = cosi_new
	rep, err := NetworkSend(ca.Private, s.Entity, response)
	if err != nil {
		return -1, -1, err
	}
	reply, ok := rep.Msg.(ResponseRet)
	if !ok {
		return -1, -1, errors.New("Didn't receive ResponseRet")
	}
	if reply.Config != nil {
		dbg.Lvl3("Received new config", *reply.Config)
		err := reply.Config.VerifySignature()
		if err != nil {
			return -1, -1, err
		}
		ca.Config = reply.Config
	} else {
		dbg.Lvl3("No new config available")
	}
	return reply.ClientsSigned, reply.ClientsTot, err
}

// AddServer adds a new server. If it's the first server and it's not used by
// another sshks, the config will be signed off and stored in that server.
// Else more than 50% of the the other clients have to sign off first.
func (ca *ClientKS) AddServer(srv *Server) error {
	dbg.Lvl3("Adding server", srv.Entity.Addresses[0], "to config", ca.Config)
	var srvSend *Server
	if len(ca.Config.Servers) == 0 {
		// If there are no servers, then there will be no
		// pre-calculated commitment ready. Send one
		dbg.Lvl3("Adding first server")
		err := ca.NetworkSendFirstCommit(srv)
		if err != nil {
			return err
		}
		srvSend = srv
		now, next, err := ca.NetworkGetConfig(srvSend)
		if err != nil {
			return err
		}
		if now.Version > 0 {
			dbg.Lvl2("Found server with available config")
			ca.Config = now
			ca.NewConfig = next
			return nil
		}
	} else {
		var err error
		srvSend, err = ca.getAnyServer()
		if err != nil {
			return err
		}
	}
	dbg.Lvl3("Adding server", srv.Entity.Addresses[0], "to config", ca.Config)
	var err error
	ca.NewConfig, err = ca.Config.Copy()
	if err != nil {
		return err
	}
	ca.NewConfig.AddServer(srv)
	err = ca.NetworkSendNewConfig(srvSend)
	if err != nil {
		return err
	}
	return ca.ConfirmNewConfig(srvSend)
}

// AddServerAddr takes an address and will ask the server for it's identity first
func (ca *ClientKS) AddServerAddr(addr string) error {
	srv, err := NetworkGetServer(addr)
	if err != nil {
		return err
	}
	return ca.AddServer(srv)
}

// DelServer deletes a server and asks the remaining servers (if any)
// to sign the new configuration
func (ca *ClientKS) DelServer(srv *Server) error {
	var err error
	ca.NewConfig, err = ca.Config.Copy()
	if err != nil {
		return err
	}
	err = ca.NewConfig.DelServer(srv)
	if err != nil {
		return err
	}
	if len(ca.NewConfig.Servers) == 0 {
		dbg.Lvl2("Deleted last server - nothing to do")
		ca.Config.Servers = make(map[string]*Server)
		return nil
	}
	srvSend, err := ca.getAnyNewServer()
	if err != nil {
		return err
	}
	dbg.Lvl3("Sending to server", srvSend.Entity.String())
	err = ca.NetworkSendNewConfig(srvSend)
	if err != nil {
		return err
	}
	return ca.ConfirmNewConfig(srvSend)
}

// DelServerAddr takes an address and will ask the server for it's identity first
func (ca *ClientKS) DelServerAddr(addr string) error {
	srv, err := NetworkGetServer(addr)
	if err != nil {
		return err
	}
	return ca.DelServer(srv)
}

// ServerCheck contacts all servers and verifies that all
// configurations are the same
func (ca *ClientKS) ServerCheck() error {
	if ca.Config == nil {
		return errors.New("No config defined")
	}
	if len(ca.Config.Servers) == 0 {
		return errors.New("No servers defined")
	}
	var cnf *Config
	for _, srv := range ca.Config.Servers {
		cnfSrv, _, err := ca.NetworkGetConfig(srv)
		dbg.ErrFatal(err)
		if cnf != nil {
			if bytes.Compare(cnf.Hash(), cnfSrv.Hash()) != 0 {
				return errors.New("Hashes should be the same\n" +
					"1st server has " + fmt.Sprintln(cnfSrv) +
					"Server" + fmt.Sprint(srv) + "has" + fmt.Sprintln(cnfSrv))
			}
			cnf = cnfSrv
		}
	}
	return nil
}

// ServerPropose sends our configuration to the server which will propose
// a new configuration including our client
func (ca *ClientKS) ServerPropose(srv *Server) error {
	msg, err := NetworkSend(ca.Private, srv.Entity, &ProposeClient{ca.This})
	dbg.Printf("%+v", msg)
	return ErrMsg(msg, err)
}

// ServerProposeAddr searches for the server at address and calls ServerPropose
func (ca *ClientKS) ServerProposeAddr(addr string) error {
	srv, err := NetworkGetServer(addr)
	if err != nil {
		return err
	}
	return ca.ServerPropose(srv)
}

// ClientAdd adds a new client and signs the new configuration
func (ca *ClientKS) AddClient(client *Client) error {
	var err error
	ca.NewConfig, err = ca.Config.Copy()
	if err != nil {
		return nil
	}
	ca.NewConfig.AddClient(client)
	if len(ca.Config.Servers) == 0 {
		// If we don't have any server, we need to save the
		// config
		ca.Config = ca.NewConfig
		return nil
	}
	srv, err := ca.getAnyServer()
	if err != nil {
		return err
	}
	err = ca.NetworkSendNewConfig(srv)
	if err != nil {
		return err
	}
	return ca.ConfirmNewConfig(srv)
}

// ClientDel deletes the client and signs the new configuration
func (ca *ClientKS) DelClient(client *Client) error {
	var err error
	ca.NewConfig, err = ca.Config.Copy()
	if err != nil {
		return nil
	}
	ca.NewConfig.DelClient(client)
	if len(ca.Config.Servers) == 0 {
		// If we don't have any server, we need to save the
		// config
		ca.Config = ca.NewConfig
		return nil
	}
	srv, err := ca.getAnyServer()
	if err != nil {
		return err
	}
	err = ca.NetworkSendNewConfig(srv)
	if err != nil {
		return err
	}
	return ca.ConfirmNewConfig(srv)
}

// Update checks for the latest configuration
// TODO: include SkipChains to get the latest cothority
func (ca *ClientKS) Update(srv *Server) error {
	needSendCommit := ca.Config.Version == 0
	conf := NewConfig(-1)
	var newconf *Config
	sendSrv := srv
	if srv == nil {
		// If no server is given, we contact all servers and ask
		// for the latest version
		dbg.Lvl4(ca.This.SSHpub, "Going to search all servers", ca.Config.Servers)
		for _, s := range ca.Config.Servers {
			c, nc, err := ca.NetworkGetConfig(s)
			if err == nil {
				if conf.Version < c.Version {
					conf = c
				}
				if nc != nil {
					dbg.Lvl3("Got new config from", s)
					newconf = nc
				}
			} else {
				dbg.Error(err)
			}
			sendSrv = s
		}
	} else {
		// If a server is given, we use that one
		dbg.Lvl3("Using server", srv, "to update")
		var err error
		conf, newconf, err = ca.NetworkGetConfig(srv)
		if err != nil {
			return err
		}
	}
	ca.Config = conf
	ca.NewConfig = newconf
	if needSendCommit {
		dbg.Lvl3("Sending first commit for client", ca.This.SSHpub, sendSrv)
		err := ca.NetworkSendFirstCommit(sendSrv)
		if err != nil {
			return err
		}
	} else if ca.NewConfig != nil {
		dbg.Lvl3("Adding challenge")
		ca.Cosi.Challenge(&cosi.Challenge{ca.NewConfig.Signature.Challenge})
	}
	return nil
}

// ConfirmNewConfig sends a response to the server, thus confirming
// that we're OK with this new configuration.
// If srv == nil, a random server is chosen.
func (ca *ClientKS) ConfirmNewConfig(srv *Server) error {
	if srv == nil {
		var err error
		srv, err = ca.getAnyServer()
		if err != nil {
			return err
		}
	}
	dbg.Lvl4("Sending to server", srv)
	sign, total, err := ca.NetworkResponse(srv)
	if err == nil {
		dbg.Lvl3("Got", sign, "out of", total, "signatures")
	}
	return err
}

// Gets any server from the config
func (ca *ClientKS) getAnyServer() (*Server, error) {
	for _, srv := range ca.Config.Servers {
		return srv, nil
	}
	return nil, errors.New("Found no servers")
}

// Gets any server from the NewConfig
func (ca *ClientKS) getAnyNewServer() (*Server, error) {
	for _, srv := range ca.NewConfig.Servers {
		return srv, nil
	}
	return nil, errors.New("Found no servers")
}
