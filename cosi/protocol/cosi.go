// Package cosi implements a round of a Collective Signing protocol.
package cosi

import (
	"sync"

	"github.com/dedis/crypto/abstract"
	"github.com/dedis/crypto/cosi"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
)

// Name can be used to reference the registered protocol.
var Name = "CoSi"

func init() {
	onet.GlobalProtocolRegister(Name, NewProtocol)
}

// This Cosi protocol is the simplest version, the "vanilla" version with the
// four phases:
//  - Announcement
//  - Commitment
//  - Challenge
//  - Response

// CoSi is the main structure holding the round and the onet.Node.
type CoSi struct {
	// The node that represents us
	*onet.TreeNodeInstance
	// TreeNodeId cached
	treeNodeID onet.TreeNodeID
	// the cosi struct we use (since it is a cosi protocol)
	// Public because we will need it from other protocols.
	cosi *cosi.CoSi
	// the message we want to sign typically given by the Root
	Message []byte
	// The channel waiting for Announcement message
	announce chan chanAnnouncement
	// the channel waiting for Commitment message
	commit chan chanCommitment
	// the channel waiting for Challenge message
	challenge chan chanChallenge
	// the channel waiting for Response message
	response chan chanResponse
	// the channel that indicates if we are finished or not
	done chan bool
	// temporary buffer of commitment messages
	tempCommitment []abstract.Point
	// lock associated
	tempCommitLock *sync.Mutex
	// temporary buffer of Response messages
	tempResponse []abstract.Scalar
	// lock associated
	tempResponseLock *sync.Mutex

	// hooks related to the various phase of the protocol.
	announcementHook AnnouncementHook
	commitmentHook   CommitmentHook
	challengeHook    ChallengeHook
	responseHook     ResponseHook
	signatureHook    SignatureHook
}

// AnnouncementHook allows for handling what should happen upon an
// announcement
type AnnouncementHook func() error

// CommitmentHook allows for handling what should happen when all
// commitments are received
type CommitmentHook func(in []abstract.Point) error

// ChallengeHook allows for handling what should happen when a
// challenge is received
type ChallengeHook func(ch abstract.Scalar) error

// ResponseHook allows for handling what should happen when all
// responses are received and our response is calculated
type ResponseHook func(in []abstract.Scalar)

// SignatureHook allows registering a handler when the signature is done
type SignatureHook func(sig []byte)

// NewProtocol returns a ProtocolCosi with the node set with the right channels.
// Use this function like this:
// ```
// round := NewRound****()
// fn := func(n *onet.Node) onet.ProtocolInstance {
//      pc := NewProtocolCosi(round,n)
//		return pc
// }
// onet.RegisterNewProtocolName("cothority",fn)
// ```
func NewProtocol(node *onet.TreeNodeInstance) (onet.ProtocolInstance, error) {
	var err error
	// XXX just need to take care to take the global list of cosigners once we
	// do the exception stuff
	publics := make([]abstract.Point, len(node.Roster().List))
	for i, e := range node.Roster().List {
		publics[i] = e.Public
	}

	c := &CoSi{
		cosi:             cosi.NewCosi(node.Suite(), node.Private(), publics),
		TreeNodeInstance: node,
		done:             make(chan bool),
		tempCommitLock:   new(sync.Mutex),
		tempResponseLock: new(sync.Mutex),
	}
	// Register the channels we want to register and listens on

	if err := node.RegisterChannel(&c.announce); err != nil {
		return c, err
	}
	if err := node.RegisterChannel(&c.commit); err != nil {
		return c, err
	}
	if err := node.RegisterChannel(&c.challenge); err != nil {
		return c, err
	}
	if err := node.RegisterChannel(&c.response); err != nil {
		return c, err
	}

	return c, err
}

// Dispatch will listen on the four channels we use (i.e. four steps)
func (c *CoSi) Dispatch() error {
	nbrChild := len(c.Children())
	if !c.IsRoot() {
		log.Lvl3(c.Name(), "Waiting for announcement")
		ann := (<-c.announce).Announcement
		err := c.handleAnnouncement(&ann)
		if err != nil {
			return err
		}
	}
	for n := 0; n < nbrChild; n++ {
		log.Lvlf3("%s Waiting for commitment of child %d/%d",
			c.Name(), n+1, nbrChild)
		commit := (<-c.commit).Commitment
		err := c.handleCommitment(&commit)
		if err != nil {
			return err
		}
	}
	if !c.IsRoot() {
		log.Lvl3(c.Name(), "Waiting for Challenge")
		challenge := (<-c.challenge).Challenge
		err := c.handleChallenge(&challenge)
		if err != nil {
			return err
		}
	}
	for n := 0; n < nbrChild; n++ {
		log.Lvlf3("%s Waiting for response of child %d/%d", c.Name(), n+1, nbrChild)
		response := (<-c.response).Response
		err := c.handleResponse(&response)
		if err != nil {
			return err
		}
	}
	<-c.done
	return nil
}

// Start will call the announcement function of its inner Round structure. It
// will pass nil as *in* message.
func (c *CoSi) Start() error {
	out := &Announcement{}
	return c.handleAnnouncement(out)
}

// VerifySignature verifies if the challenge and the secret (from the response phase) form a
// correct signature for this message using the aggregated public key.
// This is copied from cosi, so that you don't need to include both lib/cosi
// and protocols/cosi
func VerifySignature(suite abstract.Suite, publics []abstract.Point, msg, sig []byte) error {
	return cosi.VerifySignature(suite, publics, msg, sig)
}

// handleAnnouncement will pass the message to the round and send back the
// output. If in == nil, we are root and we start the round.
func (c *CoSi) handleAnnouncement(in *Announcement) error {
	log.Lvlf3("Message: %x", c.Message)
	// If we have a hook on announcement call the hook
	if c.announcementHook != nil {
		return c.announcementHook()
	}

	// If we are leaf, we should go to commitment
	if c.IsLeaf() {
		return c.handleCommitment(nil)
	}
	// send to children
	return c.SendToChildren(in)
}

// handleAllCommitment relay the commitments up in the tree
// It expects *in* to be the full set of messages from the children.
// The children's commitment must remain constants.
func (c *CoSi) handleCommitment(in *Commitment) error {
	if !c.IsLeaf() {
		// add to temporary
		c.tempCommitLock.Lock()
		c.tempCommitment = append(c.tempCommitment, in.Comm)
		c.tempCommitLock.Unlock()
		// do we have enough ?
		// TODO: exception mechanism will be put into another protocol
		if len(c.tempCommitment) < len(c.Children()) {
			return nil
		}
	}
	log.Lvl3(c.Name(), "aggregated")
	// pass it to the hook
	if c.commitmentHook != nil {
		return c.commitmentHook(c.tempCommitment)
	}

	// go to Commit()
	out := c.cosi.Commit(nil, c.tempCommitment)

	// if we are the root, we need to start the Challenge
	if c.IsRoot() {
		return c.startChallenge()
	}

	// otherwise send it to parent
	outMsg := &Commitment{
		Comm: out,
	}
	return c.SendTo(c.Parent(), outMsg)
}

// StartChallenge starts the challenge phase. Typically called by the Root ;)
func (c *CoSi) startChallenge() error {
	challenge, err := c.cosi.CreateChallenge(c.Message)
	if err != nil {
		return err
	}
	out := &Challenge{
		Chall: challenge,
	}
	log.Lvlf3("%s Starting Chal=%+v (message = %x)", c.Name(), challenge, c.Message)
	return c.handleChallenge(out)

}

// handleChallenge dispatch the challenge to the round and then dispatch the
// results down the tree.
func (c *CoSi) handleChallenge(in *Challenge) error {
	log.Lvlf3("%s chal=%+v", c.Name(), in.Chall)
	c.cosi.Challenge(in.Chall)

	if c.challengeHook != nil {
		c.challengeHook(in.Chall)
	}

	// if we are leaf, then go to response
	if c.IsLeaf() {
		return c.handleResponse(nil)
	}

	// otherwise send it to children
	return c.SendToChildren(in)
}

// handleResponse brings up the response of each node in the tree to the root.
func (c *CoSi) handleResponse(in *Response) error {
	if !c.IsLeaf() {
		// add to temporary
		c.tempResponseLock.Lock()
		c.tempResponse = append(c.tempResponse, in.Resp)
		c.tempResponseLock.Unlock()
		// do we have enough ?
		log.Lvl3(c.Name(), "has", len(c.tempResponse), "responses")
		if len(c.tempResponse) < len(c.Children()) {
			return nil
		}
	}

	defer func() {
		// protocol is finished
		close(c.done)
		c.Done()
	}()

	log.Lvl3(c.Name(), "aggregated")
	outResponse, err := c.cosi.Response(c.tempResponse)
	if err != nil {
		return err
	}

	if c.responseHook != nil {
		c.responseHook(c.tempResponse)
	}

	out := &Response{
		Resp: outResponse,
	}

	// send it back to parent
	if !c.IsRoot() {
		return c.SendTo(c.Parent(), out)
	}

	// we are root, we have the signature now
	if c.signatureHook != nil {
		c.signatureHook(c.cosi.Signature())
	}
	return nil
}

// VerifyResponses allows to check at each intermediate node whether the
// responses are valid
func (c *CoSi) VerifyResponses(agg abstract.Point) error {
	return c.cosi.VerifyResponses(agg)
}

// SigningMessage simply set the message to sign for this round
func (c *CoSi) SigningMessage(msg []byte) {
	c.Message = msg
	log.Lvlf2("%s Root will sign message %x", c.Name(), c.Message)
}

// RegisterAnnouncementHook allows for handling what should happen upon an
// announcement
func (c *CoSi) RegisterAnnouncementHook(fn AnnouncementHook) {
	c.announcementHook = fn
}

// RegisterCommitmentHook allows for handling what should happen when a
// commitment is received
func (c *CoSi) RegisterCommitmentHook(fn CommitmentHook) {
	c.commitmentHook = fn
}

// RegisterChallengeHook allows for handling what should happen when a
// challenge is received
func (c *CoSi) RegisterChallengeHook(fn ChallengeHook) {
	c.challengeHook = fn
}

// RegisterResponseHook allows for handling what should happen when a
// response is received
func (c *CoSi) RegisterResponseHook(fn ResponseHook) {
	c.responseHook = fn
}

// RegisterSignatureHook allows for handling what should happen when
// the protocol is done
func (c *CoSi) RegisterSignatureHook(fn SignatureHook) {
	c.signatureHook = fn
}
