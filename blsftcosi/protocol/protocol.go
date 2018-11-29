package protocol

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/dedis/kyber"
	"github.com/dedis/kyber/pairing"
	"github.com/dedis/kyber/pairing/bn256"
	"github.com/dedis/kyber/sign/bls"
	"github.com/dedis/kyber/sign/cosi"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
)

// VerificationFn is called on every node. Where msg is the message that is
// co-signed and the data is additional data for verification.
type VerificationFn func(msg, data []byte) bool

// init is done at startup. It defines every messages that is handled by the network
// and registers the protocols.
func init() {
	GlobalRegisterDefaultProtocols()
}

// BlsFtCosi holds the parameters of the protocol.
// It also defines a channel that will receive the final signature.
// This protocol should only exist on the root node.
type BlsFtCosi struct {
	*onet.TreeNodeInstance
	Msg            []byte
	Data           []byte
	CreateProtocol CreateProtocolFunction
	// Timeout is not a global timeout for the protocol, but a timeout used
	// for waiting for responses for sub protocols.
	Timeout        time.Duration
	Threshold      int
	FinalSignature chan BlsSignature // final signature that is sent back to client

	stoppedOnce     sync.Once
	subProtocols    []*SubBlsFtCosi
	startChan       chan bool
	subProtocolName string
	verificationFn  VerificationFn
	suite           pairing.Suite
	subTrees        BlsProtocolTree
}

// CreateProtocolFunction is a function type which creates a new protocol
// used in FtCosi protocol for creating sub leader protocols.
type CreateProtocolFunction func(name string, t *onet.Tree) (onet.ProtocolInstance, error)

// NewDefaultProtocol is the default protocol function used for registration
// with an always-true verification.
// Called by GlobalRegisterDefaultProtocols
func NewDefaultProtocol(n *onet.TreeNodeInstance) (onet.ProtocolInstance, error) {
	vf := func(a, b []byte) bool { return true }
	return NewBlsFtCosi(n, vf, DefaultSubProtocolName, bn256.NewSuiteG2())
}

// GlobalRegisterDefaultProtocols is used to register the protocols before use,
// most likely in an init function.
func GlobalRegisterDefaultProtocols() {
	onet.GlobalProtocolRegister(DefaultProtocolName, NewDefaultProtocol)
	onet.GlobalProtocolRegister(DefaultSubProtocolName, NewDefaultSubProtocol)
}

// NewBlsFtCosi method is used to define the ftcosi protocol.
func NewBlsFtCosi(n *onet.TreeNodeInstance, vf VerificationFn, subProtocolName string, suite pairing.Suite) (onet.ProtocolInstance, error) {
	c := &BlsFtCosi{
		TreeNodeInstance: n,
		FinalSignature:   make(chan BlsSignature, 1),
		startChan:        make(chan bool, 1),
		verificationFn:   vf,
		subProtocolName:  subProtocolName,
		suite:            suite,
	}

	if len(n.Roster().List) > 1 {
		c.SetNbrSubTree(1)
	}

	return c, nil
}

// SetNbrSubTree generatesN new subtrees that will be used
// for the protocol
func (p *BlsFtCosi) SetNbrSubTree(nbr int) error {
	if nbr > len(p.Roster().List)-1 {
		return errors.New("Cannot have more subtrees than nodes")
	}
	if p.Threshold == 1 || nbr <= 0 {
		p.subTrees = []*onet.Tree{}
		return nil
	}

	var err error
	p.subTrees, err = NewBlsProtocolTree(p.Tree(), nbr)
	if err != nil {
		p.FinalSignature <- nil
		return fmt.Errorf("error in tree generation: %s", err)
	}

	return nil
}

// Shutdown stops the protocol
func (p *BlsFtCosi) Shutdown() error {
	p.stoppedOnce.Do(func() {
		for _, subFtCosi := range p.subProtocols {
			// we're stopping the root thus it will stop the children
			// by itself
			subFtCosi.Shutdown()
		}
		close(p.startChan)
		close(p.FinalSignature)
	})
	return nil
}

// Dispatch is the main method of the protocol, defining the root node behaviour
// and sequential handling of subprotocols.
func (p *BlsFtCosi) Dispatch() error {
	defer p.Done()
	if !p.IsRoot() {
		return nil
	}

	select {
	case _, ok := <-p.startChan:
		if !ok {
			return errors.New("protocol finished prematurely")
		}
	case <-time.After(time.Second):
		return fmt.Errorf("timeout, did you forget to call Start?")
	}

	log.Lvl3("root protocol started")

	// Verification of the data
	verifyChan := make(chan bool, 1)
	go func() {
		log.Lvl3(p.ServerIdentity().Address, "starting verification")
		verifyChan <- p.verificationFn(p.Msg, p.Data)
	}()

	// start all subprotocols
	p.subProtocols = make([]*SubBlsFtCosi, len(p.subTrees))
	for i, tree := range p.subTrees {
		log.Lvl2("Invoking start sub protocol", tree)
		var err error
		p.subProtocols[i], err = p.startSubProtocol(tree)
		if err != nil {
			log.Error(err)
			return err
		}
	}
	log.Lvl3(p.ServerIdentity().Address, "all protocols started")

	// Wait and collect all the signature responses
	responses, err := p.collectSignatures()
	if err != nil {
		return err
	}
	log.Lvl3(p.ServerIdentity().Address, "collected all signature responses")

	// verifies the proposal
	var verificationOk bool
	select {
	case verificationOk = <-verifyChan:
		close(verifyChan)
	case <-time.After(p.Timeout):
		log.Error(p.ServerIdentity(), "timeout while waiting for the verification!")
	}
	if !verificationOk {
		// root should not fail the verification otherwise it would not have started the protocol
		return fmt.Errorf("verification failed on root node")
	}

	// generate root signature
	signaturePoint, finalMask, err := p.generateSignature(responses, verificationOk)
	if err != nil {
		return err
	}

	signature, err := signaturePoint.MarshalBinary()
	if err != nil {
		return err
	}

	finalSignature := append(signature, finalMask.Mask()...)

	log.Lvl3(p.ServerIdentity().Address, "Created final signature", signature, finalMask, finalSignature)

	p.FinalSignature <- finalSignature

	log.Lvl3("Root-node is done without errors")
	return nil

}

// Start is done only by root and starts the protocol.
// It also verifies that the protocol has been correctly parameterized.
func (p *BlsFtCosi) Start() error {
	err := p.checkIntegrity()
	if err != nil {
		p.Done()
		return err
	}

	log.Lvl3("Starting CoSi")
	p.startChan <- true
	return nil
}

// checkIntegrity checks if the protocol has been instantiated with
// correct parameters
func (p *BlsFtCosi) checkIntegrity() error {
	if p.Msg == nil {
		return fmt.Errorf("no proposal msg specified")
	}
	if p.CreateProtocol == nil {
		return fmt.Errorf("no create protocol function specified")
	}
	if p.verificationFn == nil {
		return fmt.Errorf("verification function cannot be nil")
	}
	if p.subProtocolName == "" {
		return fmt.Errorf("sub-protocol name cannot be empty")
	}
	if p.Timeout < 10*time.Nanosecond {
		return fmt.Errorf("unrealistic timeout")
	}
	if p.Threshold > p.Tree().Size() {
		return fmt.Errorf("threshold (%d) bigger than number of nodes (%d)", p.Threshold, p.Tree().Size())
	}
	if p.Threshold < 1 {
		return fmt.Errorf("threshold of %d smaller than one node", p.Threshold)
	}

	return nil
}

func (p *BlsFtCosi) checkFailureThreshold(numFailure int) bool {
	if numFailure == 0 {
		return false
	}

	return numFailure > len(p.Roster().List)-p.Threshold
}

// startSubProtocol creates, parametrize and starts a subprotocol on a given tree
// and returns the started protocol.
func (p *BlsFtCosi) startSubProtocol(tree *onet.Tree) (*SubBlsFtCosi, error) {

	pi, err := p.CreateProtocol(p.subProtocolName, tree)
	if err != nil {
		return nil, err
	}
	cosiSubProtocol := pi.(*SubBlsFtCosi)
	cosiSubProtocol.Msg = p.Msg
	cosiSubProtocol.Data = p.Data
	cosiSubProtocol.Timeout = p.Timeout / 2

	//the Threshold (minus root node) is divided evenly among the subtrees
	subThreshold := int(math.Ceil(float64(p.Threshold-1) / float64(len(p.subTrees))))
	if subThreshold > tree.Size()-1 {
		subThreshold = tree.Size() - 1
	}

	cosiSubProtocol.Threshold = subThreshold

	log.Lvl2("Starting sub protocol on", tree)
	err = cosiSubProtocol.Start()
	if err != nil {
		return nil, err
	}

	return cosiSubProtocol, err
}

// Collect signatures from each sub-leader, restart whereever sub-leaders fail to respond.
// The collected signatures are already aggregated for a particular group
func (p *BlsFtCosi) collectSignatures() (ResponseMap, error) {
	responsesChan := make(chan StructResponse, len(p.subProtocols))
	errChan := make(chan error, len(p.subProtocols))
	closeChan := make(chan bool)

	for i, subProtocol := range p.subProtocols {
		go func(i int, subProtocol *SubBlsFtCosi) {
			timeout := time.After(p.Timeout)
			for {
				select {
				case <-closeChan:
					// quick answer/failure
					return
				case <-subProtocol.subleaderNotResponding:
					subleaderID := p.subTrees[i].Root.Children[0].RosterIndex
					log.Lvlf2("(subprotocol %v) subleader with id %d failed, restarting subprotocol", i, subleaderID)

					// generate new tree by adding the current subleader to the end of the
					// leafs and taking the first leaf for the new subleader.
					nodes := []int{p.subTrees[i].Root.RosterIndex}
					for _, child := range p.subTrees[i].Root.Children[0].Children {
						nodes = append(nodes, child.RosterIndex)
					}

					if len(nodes) < 2 || subleaderID > nodes[1] {
						errChan <- fmt.Errorf("(subprotocol %v) failed with every subleader, ignoring this subtree",
							i)
						return
					}
					nodes = append(nodes, subleaderID)

					var err error
					p.subTrees[i], err = genSubtree(p.subTrees[i].Roster, nodes)
					if err != nil {
						errChan <- fmt.Errorf("(subprotocol %v) error in tree generation: %v", i, err)
						return
					}

					// restart subprotocol
					// send stop signal to old protocol
					subProtocol.HandleStop(StructStop{subProtocol.TreeNode(), Stop{}})
					subProtocol, err = p.startSubProtocol(p.subTrees[i])
					if err != nil {
						errChan <- fmt.Errorf("(subprotocol %v) error in restarting of subprotocol: %s", i, err)
						return
					}

					p.subProtocols[i] = subProtocol
				case response := <-subProtocol.subResponse:
					responsesChan <- response
					return
				case <-timeout:
					// This should never happen, as the subProto should return before that
					// timeout, even if it didn't receive enough responses.
					errChan <- fmt.Errorf("timeout should not happen while waiting for response: %d", i)
					return
				}
			}
		}(i, subProtocol)
	}

	// handle answers from all parallel threads
	responseMap := make(ResponseMap)
	numSignature := 0
	numFailure := 0
	for len(p.subProtocols) > 0 && numSignature < p.Threshold-1 && !p.checkFailureThreshold(numFailure) {
		select {
		case res := <-responsesChan:
			mask, err := cosi.NewMask(p.suite.(cosi.Suite), p.Roster().Publics(), nil)
			if err != nil {
				return nil, err
			}
			err = mask.SetMask(res.Mask)
			if err != nil {
				return nil, err
			}

			numSignature += mask.CountEnabled()
			numFailure += res.SubtreeCount() - mask.CountEnabled()
			responseMap[res.ID] = &res.Response
		case err := <-errChan:
			err = fmt.Errorf("error in getting responses: %s", err)
			return nil, err
		case <-time.After(p.Timeout):
			return nil, fmt.Errorf("not enough replies from nodes at timeout %v "+
				"for Threshold %d, got %d responses for %d requests", p.Timeout,
				p.Threshold, numSignature, len(p.Roster().List)-1)
		}
	}

	// Force to stop pending subprotocols in case of quick answer/failure scenario
	close(closeChan)

	if p.checkFailureThreshold(numFailure) {
		return nil, fmt.Errorf("too many refusals (got %d), the threshold of %d cannot be achieved",
			numFailure, p.Threshold)
	}

	return responseMap, nil
}

// Sign the message with this node and aggregates with all child signatures (in structResponses)
// Also aggregates the child bitmasks
func (p *BlsFtCosi) generateSignature(responses ResponseMap, ok bool) (kyber.Point, *cosi.Mask, error) {
	publics := p.Roster().Publics()

	//generate personal mask
	var personalMask *cosi.Mask
	if ok {
		personalMask, _ = cosi.NewMask(p.suite.(cosi.Suite), publics, p.Public())
	} else {
		personalMask, _ = cosi.NewMask(p.suite.(cosi.Suite), publics, nil)
	}

	// generate personal signature and append to other sigs
	personalSig, err := bls.Sign(p.suite, p.Private(), p.Msg)
	if err != nil {
		return nil, nil, err
	}

	// fill the map with the Root signature
	responses[p.TreeNode().ID] = &Response{
		Mask:      personalMask.Mask(),
		Signature: personalSig,
	}

	// Aggregate all signatures
	response, err := makeAggregateResponse(p.suite, publics, responses)
	if err != nil {
		log.Lvlf3("%v failed to create aggregate signature", p.ServerIdentity())
		return nil, nil, err
	}

	//create final aggregated mask
	finalMask, err := cosi.NewMask(p.suite.(cosi.Suite), publics, nil)
	if err != nil {
		return nil, nil, err
	}
	err = finalMask.SetMask(response.Mask)
	if err != nil {
		return nil, nil, err
	}

	finalSignature, err := response.Signature.Point(p.suite)
	if err != nil {
		return nil, nil, err
	}
	log.Lvlf3("%v is done aggregating signatures with total of %d signatures", p.ServerIdentity(), len(responses))

	return finalSignature, finalMask, err
}
