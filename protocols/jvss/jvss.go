package jvss

import (
	"errors"
	"github.com/dedis/cothority/lib/cliutils"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/sda"
	"github.com/dedis/crypto/abstract"
	"github.com/dedis/crypto/config"
	"github.com/dedis/crypto/poly"
	"github.com/satori/go.uuid"
	"hash"
	"sync"
)

// SDA-based JVSS , a port of app/shamir

// JVSS Protocol Instance structure holding the information for a longterm JVSS
// signing mechanism
type JVSSProtocol struct {
	// The TreeNode denotating ourself in the tree
	Node *sda.Node
	// The EntityList we are using / this is needed to "bypass" the tree
	// structure for the internals communication, when we set up the shares and
	// everything. We directly send our share to everyone else directly by using
	// this entitylist instead of broadcasting into the tree.
	List *sda.EntityList
	// the index where we are in this entitylist
	index int
	// the mapping between TreeNode's peer id in the Tree to index in the entitylist,
	// since JVSS mostly use the entityList
	nodeToIndex map[uuid.UUID]int
	// list of public keys represented in the entityList (needed by poly.Deal)
	publicList []abstract.Point
	// keys of the Host set as config.KeyPair
	key config.KeyPair

	info poly.Threshold
	// The channel where we give the deal we receive for the longterm
	// generation
	ltChan chan LongtermChan
	// The channel through we give the deal we receive for the random generation
	rdChan chan RandomChan
	// channel where we give the requests we receive for a signature
	reqChan chan RequestChan
	// channel where we give the responses we receive for a signature request
	respChan chan ResponseChan
	// requests holds all the requests that we asked
	requests map[int]*RequestBuffer
	// lastrequestnumber seen or executed
	lastRequestNo int

	longterm     *LongtermRequest
	longtermLock *sync.Mutex

	// callback to know when the longterm has been generated
	onLongtermDone func(*poly.SharedSecret)
}

type LongtermRequest struct {
	// The longterm shared private public key pair used in this JVSS.
	// The idea is that you can keep this protocol instance as a longterm JVSS
	// using it to distributively sign anything as long as it runs.
	secret *poly.SharedSecret
	// The schnorr struct used to sign / verify using the longterm key
	schnorr *poly.Schnorr
	// Threshold related to how the shares are generated and reconstructed
	info poly.Threshold
	// suite used
	suite abstract.Suite
	// key from this node
	key config.KeyPair
	// longterm-receiver of the deals
	receiver *poly.Receiver
	// doneChan
	doneChan chan bool
	// done flag
	done bool
	// done lock
	doneLock *sync.Mutex
	// how many deals are ok
	goodDeal int
}

// NewJVSSProtocol returns a JVSSProtocol with the fields set. You can then
// change the fields or set specific ones etc. If you want to use JVSSProotocol
// directly with SDA, you just need to  register this function:
// ```func(h,t,tok) ProtocolInstance  { return NewJVSSProtocol(h,t,tok) }```
// For example, this function returns a JVSSProtocol with a default
// poly.Treshold. You can give a new one after calling this function.
func NewJVSSProtocol(n *sda.Node) (*JVSSProtocol, error) {
	// find ourself in the entityList
	var idx int = -1
	// at the same time create the public list
	tree := n.Tree()
	pubs := make([]abstract.Point, len(tree.EntityList.List))
	for i := range tree.EntityList.List {
		ent := tree.EntityList.Get(i)
		if ent.Equal(n.Entity()) {
			idx = i
		}
		pubs[i] = ent.Public
	}
	// map the index
	maps := make(map[uuid.UUID]int)
	nodes := tree.ListNodes()
	for i := range tree.EntityList.List {
		for _, n := range nodes {
			if n.Entity.Equal(tree.EntityList.Get(i)) {
				maps[n.Id] = i
			}
		}
	}
	if idx == -1 {
		panic("Could not find this JVSSProtocol into the EntityList")
	}
	kp := config.KeyPair{Public: n.Entity().Public, Secret: n.Private(), Suite: n.Suite()}
	nbPeers := len(tree.EntityList.List)
	info := poly.Threshold{T: nbPeers, R: nbPeers, N: nbPeers}
	jv := &JVSSProtocol{
		Node:         n,
		List:         tree.EntityList,
		index:        idx,
		info:         info,
		publicList:   pubs,
		key:          kp,
		requests:     make(map[int]*RequestBuffer),
		nodeToIndex:  maps,
		ltChan:       make(chan LongtermChan),
		rdChan:       make(chan RandomChan),
		reqChan:      make(chan RequestChan),
		respChan:     make(chan ResponseChan),
		longtermLock: new(sync.Mutex),
		longterm:     NewLongtermRequest(n.Suite(), info, kp),
	}
	if err := jv.Node.RegisterChannel(jv.ltChan); err != nil {
		return nil, err
	}
	go jv.waitForLongterm()
	if err := jv.Node.RegisterChannel(jv.rdChan); err != nil {
		return nil, err
	}
	go jv.waitForRandom()
	if err := jv.Node.RegisterChannel(jv.reqChan); err != nil {
		return nil, err
	}
	go jv.waitForRequests()
	if err := jv.Node.RegisterChannel(jv.respChan); err != nil {
		return nil, err
	}
	go jv.waitForResponses()
	return jv, nil
}

func NewJVSSProtocolInstance(node *sda.Node) (sda.ProtocolInstance, error) {
	return NewJVSSProtocol(node)
}

// Start will send the message to first compute the long term secret
// It's a blocking call  because we are supposed to launch that into a go
// routine anyway from sda.
func (jv *JVSSProtocol) Start() error {
	jv.waitLongtermSecret()
	return nil
}

func (jv *JVSSProtocol) waitLongtermSecret() {
	// add our own deal
	deal := jv.newDeal()
	jv.longterm.AddDeal(jv.index, deal)

	lt := NewLongtermFromDeal(jv.index, deal)
	// send the deal to everyone
	jv.otherNodes(func(tn *sda.TreeNode) {
		jv.Node.SendTo(tn, &lt)
	})

	// and wait
	jv.longterm.WaitDone()
	dbg.Lvl3("JVSS (", jv.index, ") Longtern Generated!")
	// callbacks !
	if jv.onLongtermDone != nil {
		jv.onLongtermDone(jv.longterm.secret)
	}
}

func (jv *JVSSProtocol) Dispatch(msgs []*sda.SDAData) error {
	panic("JVSS dont use dispatch method")
	return nil
}

// Verify returns true if a signature is valid or not
func (jv *JVSSProtocol) Verify(msg []byte, sig *poly.SchnorrSig) error {
	return jv.longterm.Verify(msg, sig)
}

// Sign will make the JVSS protocol run and returns a SchnorrSig
func (jv *JVSSProtocol) Sign(msg []byte) (*poly.SchnorrSig, error) {
	// create new request number to generate random then signature
	request, err := jv.setupDistributedSecret()
	if err != nil {
		return nil, err
	}
	sigChan := make(chan *poly.SchnorrSig)
	request.SetSigChan(sigChan)
	// add our own partial sig
	if err := request.startNewSigningRequest(msg); err != nil {
		return nil, err
	}
	dbg.Lvl3("Started NewRound with longterm.Pub:", jv.longterm)
	// create signature request
	req := &SignatureRequest{
		RequestNo: request.requestNo,
		Msg:       msg,
	}
	dbg.Lvl3("JVSS (", jv.index, ") Sending Signature Request (", request.Nb(), ")")

	// sends it
	jv.otherNodes(func(tn *sda.TreeNode) {
		jv.Node.SendTo(tn, req)
	})
	// wait for the signature
	sig := <-sigChan
	request.ResetSigChan()
	return sig, nil
}

func (jv *JVSSProtocol) waitForResponses() {
	for st := range jv.respChan {
		dbg.Lvl3("JVSS (", jv.index, ") Received Response")
		sigResponse := st.SignatureResponse
		var requestBuff *RequestBuffer
		var ok bool
		if requestBuff, ok = jv.requests[sigResponse.RequestNo]; !ok {
			// Not good, someone asks for a request we did not produce a shared
			// secret before .. ??
			dbg.Error("Received signature request with request number not matching any shared secret...")
			continue
		}
		requestBuff.AddSignatureResponse(sigResponse)
	}

}

// waitForRandom simply receives Random messages and put them in the appropriate
// RandomBuffer.
func (jv *JVSSProtocol) waitForRandom() {
	for st := range jv.rdChan {
		dbg.Lvl3("JVSS (", jv.index, ") Received Random ")
		random := st.Random
		var reqBuff *RequestBuffer
		var ok bool
		if reqBuff, ok = jv.requests[random.RequestNo]; !ok {
			// we didn't started this new shared secret request so we should
			// pariticipate in.
			reqBuff = jv.initRequestBuffer(random.RequestNo)
			dbg.Lvl3("JVSS (", jv.index, ") Received Request for random (", random.RequestNo, ")")
			go jv.handleRequestSecret(reqBuff)
		}
		reqBuff.AddRandom(random)
	}
}

// waitForRequests waits for SignatureRequest message. It checks if we have the
// generated random for this request number and if so sends back a partialSig to
// the origin of the request.
func (jv *JVSSProtocol) waitForRequests() {
	for st := range jv.reqChan {
		dbg.Lvl3("JVSS (", jv.index, ") received Request")
		sigRequest := st.SignatureRequest
		var requestBuff *RequestBuffer
		var ok bool
		if requestBuff, ok = jv.requests[sigRequest.RequestNo]; !ok {
			// Not good, someone ask for a request we did not produce a shared
			// secret before .. ??
			dbg.Error("Receive signature request with request number nto matching any shared secret...")
			continue
		}
		if requestBuff.secret == nil {
			dbg.Error("JVSS (", jv.index, ") Received signature request (", sigRequest.RequestNo, ") with no secret generated :/")
			continue
		}
		jv.longtermLock.Lock()
		if !jv.longterm.isDone() {
			jv.longtermLock.Unlock()
			dbg.Error("JVSS (", jv.index, ") Received signature request (", sigRequest.RequestNo, ") without even the longterm secret set")
			continue
		}
		jv.longtermLock.Unlock()
		dbg.Lvl3("Started NewRound with secret.Pub", requestBuff.secret.Pub)
		dbg.Lvl3("Started NewRound with longerm.Pub", jv.longterm.secret.Pub)
		// get the partial sig
		ps := requestBuff.onNewSigningRequest(sigRequest.Msg)
		if ps == nil {
			dbg.Error("Can not start new round")
			continue
		}
		sr := &SignatureResponse{
			RequestNo: sigRequest.RequestNo,
			Partial:   ps,
		}
		// send it back to the originator
		if err := jv.Node.SendTo(&st.TreeNode, sr); err != nil {
			dbg.Lvl3("Could not send signature response back", err)
		}
		dbg.Lvl3("JVSS (", jv.index, ") Sent SignatureResponse back")
	}
}

// waitForLongterm waits on a channel that receive every deals to be accepted
// for computeing the longterm distributed secret
func (jv *JVSSProtocol) waitForLongterm() {
	for st := range jv.ltChan {
		lt := st.Longterm
		// if this is our index, that means we already setup the longterm
		// receiver.otherwise, roll along with the other peers
		jv.longtermLock.Lock()
		isNew := jv.longterm.isNew()
		jv.longtermLock.Unlock()
		if isNew {
			// setup the longterm with the others peers
			go jv.waitLongtermSecret()
		}
		dbg.Lvl3("JVSS (", jv.index, ") Received longterm (", lt.Index, ")")
		deal := lt.Deal(jv.Node.Suite(), jv.info)
		jv.longterm.AddDeal(jv.index, deal)
	}
}

// setupDistributedSecret is called by the leader or the iniator that wants to
// start a new round, a new signing request, where we must first create a random
// distributed secret
func (jv *JVSSProtocol) setupDistributedSecret() (*RequestBuffer, error) {
	jv.lastRequestNo++
	req := jv.initRequestBuffer(jv.lastRequestNo)
	return jv.handleRequestSecret(req)

}

// setupRequestSecret sets up the random distributed secret for this request
// number. When the initiator starts a new request,peers will call this function
// so they also get the random dis. secret.
func (jv *JVSSProtocol) handleRequestSecret(requestBuff *RequestBuffer) (*RequestBuffer, error) {
	// prepare our deal
	doneChan := make(chan *poly.SharedSecret)
	requestBuff.SetSecretChan(doneChan)
	deal := jv.newDeal()
	requestBuff.AddDeal(jv.index, deal)
	// send to everyone
	buf, err := deal.MarshalBinary()
	if err != nil {
		return nil, err
	}

	jv.otherNodes(func(tn *sda.TreeNode) {
		rand := Random{
			RequestNo: requestBuff.Nb(),
			Longterm: Longterm{
				Bytes: buf,
				Index: jv.nodeToIndex[tn.Id],
			},
		}
		jv.Node.SendTo(tn, &rand)
	})
	// wait for the shared secret
	_ = <-doneChan
	requestBuff.ResetSecretChan()

	return requestBuff, nil

}

func (jv *JVSSProtocol) newDeal() *poly.Deal {
	dealKey := cliutils.KeyPair(jv.Node.Suite())
	deal := new(poly.Deal).ConstructDeal(&dealKey, &jv.key, jv.info.T, jv.info.R, jv.publicList)
	return deal
}

// RequestBuffer holds every info for the many distributed secrets we may need to
// compute in parallel. It also holds the partials signatures related to this
// request used for signing.
type RequestBuffer struct {
	// for which request number this buffer is
	requestNo int
	// The deals we have received so far for generating this rndom secret
	goodDeal int
	dealLock *sync.Mutex
	// the receiver aggregating them
	receiver *poly.Receiver
	// the generated secret if any
	secret *poly.SharedSecret
	// generated secret flag
	secretGend bool
	// channel to say the random secret has been generated
	secretChan chan *poly.SharedSecret
	// the channel to say the final signature related has been generated
	sigChan chan *poly.SchnorrSig
	// generated signature flag
	sigGend bool
	// The partial signatures aggregated until now
	goodPartials int
	partialLock  *sync.Mutex
	// the longterm schnorr struct used to sign
	longterm *LongtermRequest
	// the signature itself
	signature *poly.SchnorrSig
	// the info about the JVSS config
	info poly.Threshold
	// the suite used
	suite abstract.Suite
}

// startNewSigningRequest start a new round and adds its own signature to the
// schnorr struct so later it could reveal the final signature.
func (rb *RequestBuffer) startNewSigningRequest(msg []byte) error {
	ps := rb.onNewSigningRequest(msg)
	if ps == nil {
		return errors.New("Could not generate partial signature")
	}
	err := rb.longterm.schnorr.AddPartialSig(ps)
	return err
}

// onNewSigningRequest simply starts a new round and returns the partial
// signature this schnorr can offer to the gloabal signature. Mostly used by
// servers that receive the request to sign something.
func (rb *RequestBuffer) onNewSigningRequest(msg []byte) *poly.SchnorrPartialSig {
	h := rb.suite.Hash()
	h.Write(msg)
	dbg.Lvl3("NewSigningRequest with secret.Pub:", rb.secret.Pub)
	ps := rb.longterm.NewSigning(rb.secret, h)
	return ps
}

// Nb returns the request number associated with this request buffer
func (rb *RequestBuffer) Nb() int {
	return rb.requestNo
}

func (rb *RequestBuffer) SetSecretChan(ch chan *poly.SharedSecret) {
	rb.secretChan = ch
}
func (rb *RequestBuffer) ResetSecretChan() {
	close(rb.secretChan)
	rb.secretChan = nil
}

func (rb *RequestBuffer) SetSigChan(ch chan *poly.SchnorrSig) {
	rb.sigChan = ch
}
func (rb *RequestBuffer) ResetSigChan() {
	close(rb.sigChan)
	rb.sigChan = nil
}

// AddDeal is same as AddRandom but for Deal  (struct vs []byte)
func (rb *RequestBuffer) AddDeal(index int, deal *poly.Deal) {
	_, err := rb.receiver.AddDeal(index, deal)
	if err != nil {
		dbg.Error("Could not add deal", err)
		return
	}
	rb.dealLock.Lock()
	rb.goodDeal++
	if rb.goodDeal >= rb.info.T {
		// did we already generated it
		if !rb.secretGend {
			sh, err := rb.receiver.ProduceSharedSecret()
			if err != nil {
				dbg.Error("Could not produce shared secret:", err)
				return
			}
			dbg.Lvl3("JVSS (", index, ") Generated Shared Secret for request (", rb.requestNo, ")")
			rb.secret = sh
			rb.secretGend = true
		}
		// notify any interested party
		if rb.secretChan != nil {
			go func() { rb.secretChan <- rb.secret }()
		}
	}
	rb.dealLock.Unlock()
}

// AddRandom add the RandomMessage and check if we can generate the secret
// already
func (rb *RequestBuffer) AddRandom(rand Random) {
	if rand.RequestNo != rb.requestNo {
		return
	}
	deal := rand.Deal(rb.suite, rb.info)
	rb.AddDeal(rand.Index, deal)

}

func (rb *RequestBuffer) AddSignatureResponse(partialSig SignatureResponse) {
	if partialSig.RequestNo != rb.requestNo {
		return
	}

	if err := rb.longterm.schnorr.AddPartialSig(partialSig.Partial); err != nil {
		dbg.Error("Could not add partial signature(", partialSig.Partial.Index, ") to request buffer", err)
		return
	}
	rb.partialLock.Lock()
	rb.goodPartials++
	if rb.goodPartials >= rb.info.T-1 {
		if !rb.sigGend {
			sign, err := rb.longterm.schnorr.Sig()
			if err != nil {
				dbg.Error("Could not generated final signature:", err)
				return
			}
			dbg.Lvl3("JVSS (", ") Generated Signature Response")
			rb.signature = sign
			rb.sigGend = true
			// notify interested party
			if rb.sigChan != nil {
				go func() { rb.sigChan <- sign }()
			}
		}
	}
	rb.partialLock.Unlock()
}

// initrequestBuffer init a random buffer for this request number
func (jv *JVSSProtocol) initRequestBuffer(rNo int) *RequestBuffer {
	rd := &RequestBuffer{
		requestNo:   rNo,
		receiver:    poly.NewReceiver(jv.Node.Suite(), jv.info, &jv.key),
		longterm:    jv.longterm,
		secretChan:  nil,
		sigChan:     nil,
		info:        jv.info,
		suite:       jv.Node.Suite(),
		dealLock:    new(sync.Mutex),
		partialLock: new(sync.Mutex),
	}
	jv.requests[rNo] = rd
	return rd
}

func (jv *JVSSProtocol) otherNodes(fn func(*sda.TreeNode)) {
	if !jv.Node.Root().Entity.Equal(jv.Node.Entity()) {
		fn(jv.Node.Root())
	}
	for _, tn := range jv.Node.Root().Children {
		if !tn.Entity.Equal(jv.Node.Entity()) {
			fn(tn)
		}
	}
}

func (jv *JVSSProtocol) RegisterOnLongtermDone(fn func(*poly.SharedSecret)) {
	jv.onLongtermDone = fn
}

func NewLongtermRequest(suite abstract.Suite, info poly.Threshold, key config.KeyPair) *LongtermRequest {
	return &LongtermRequest{
		schnorr:  new(poly.Schnorr),
		info:     info,
		key:      key,
		receiver: poly.NewReceiver(suite, info, &key),
		done:     false,
		doneChan: make(chan bool),
		doneLock: new(sync.Mutex),
		suite:    suite,
	}
}

func (lr *LongtermRequest) AddDeal(index int, deal *poly.Deal) {
	if _, err := lr.receiver.AddDeal(index, deal); err != nil {
		dbg.Error("Error adding deal to longterm receiver", err)
		return
	}
	lr.goodDeal++
	lr.checkState()
}

// checkState will look if we have enough deals for the longterm, and if so,will
// create the shared secret and signify that we are done
func (lr *LongtermRequest) checkState() {
	if lr.goodDeal < lr.info.T {
		return
	}
	lr.doneLock.Lock()
	if lr.done == true {
		return
	}
	sh, err := lr.receiver.ProduceSharedSecret()
	if err != nil {
		dbg.Error("Could not produce shared secret", err)
		return
	}
	lr.secret = sh
	lr.schnorr.Init(lr.suite, lr.info, lr.secret)
	// notify we have the longterm secret
	lr.done = true
	lr.doneLock.Unlock()
	go func() { lr.doneChan <- true }()
}

func (lr *LongtermRequest) WaitDone() {
	<-lr.doneChan
}
func (lr *LongtermRequest) isDone() bool {
	var ret bool
	lr.doneLock.Lock()
	ret = lr.done
	lr.doneLock.Unlock()
	return ret
}

func (lr *LongtermRequest) isNew() bool {
	if lr.goodDeal == 0 {
		return true
	}
	return false
}

func (lr *LongtermRequest) NewSigning(random *poly.SharedSecret, msg hash.Hash) *poly.SchnorrPartialSig {
	if err := lr.schnorr.NewRound(random, msg); err != nil {
		dbg.Error("NewRound error:", err)
		return nil
	}
	// reveal signature and add its own
	ps := lr.schnorr.RevealPartialSig()
	return ps
}

func (lr *LongtermRequest) Verify(msg []byte, sig *poly.SchnorrSig) error {
	h := lr.suite.Hash()
	h.Write(msg)
	return lr.schnorr.VerifySchnorrSig(sig, h)
}
