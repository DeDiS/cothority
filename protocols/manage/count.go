package manage

import (
	"time"

	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	"github.com/dedis/cothority/lib/sda"
	"sync"
)

/*
The count-protocol returns the number of nodes reachable in a given
timeout. To correctly wait for the whole tree, every node that receives
a message sends a message to the root before contacting its children.
As long as the root receives those messages, he knows the counting
still goes on.
*/

func init() {
	network.RegisterMessageType(PrepareCount{})
	network.RegisterMessageType(Count{})
	network.RegisterMessageType(NodeIsUp{})
	sda.RegisterNewProtocol("Count", NewCount)
}

// ProtocolCount holds all channels. If a timeout occurs or the counting
// is done, the Count-channel receives the number of nodes reachable in
// the tree.
type ProtocolCount struct {
	*sda.Node
	Replies          int
	Count            chan int
	Quit             chan bool
	timeout          int
	timeoutMu        sync.Mutex
	PrepareCountChan chan struct {
		*sda.TreeNode
		PrepareCount
	}
	CountChan    chan []CountMsg
	NodeIsUpChan chan struct {
		*sda.TreeNode
		NodeIsUp
	}
}

// PrepareCount is sent so that every node can contact the root to say
// the counting is still going on.
type PrepareCount struct {
	Timeout int
}

// NodeIsUp - if it is received by the root it will reset the counter.
type NodeIsUp struct{}

// Count sends the number of children to the parent node.
type Count struct {
	Children int
}

// CountMsg is wrapper around the Count-structure
type CountMsg struct {
	*sda.TreeNode
	Count
}

// NewCount returns a new protocolInstance
func NewCount(n *sda.Node) (sda.ProtocolInstance, error) {
	p := &ProtocolCount{
		Node:    n,
		Quit:    make(chan bool),
		timeout: 1024,
	}
	p.Count = make(chan int, 1)
	p.RegisterChannel(&p.CountChan)
	p.RegisterChannel(&p.PrepareCountChan)
	p.RegisterChannel(&p.NodeIsUpChan)
	return p, nil
}

// Start the protocol
func (p *ProtocolCount) Start() error {
	// Send an empty message
	dbg.Lvl3("Starting to count")
	p.FuncPC()
	return nil
}

// Dispatch listens for all channels and waits for a timeout in case nothing
// happens for a certain duration
func (p *ProtocolCount) Dispatch() error {
	running := true
	for running {
		dbg.Lvl3(p.Info(), "waiting for message during", p.Timeout())
		select {
		case pc := <-p.PrepareCountChan:
			dbg.Lvl3(p.Info(), "received from", pc.TreeNode.Entity.Addresses,
				pc.Timeout)
			p.SetTimeout(pc.Timeout)
			p.FuncPC()
		case c := <-p.CountChan:
			p.FuncC(c)
			running = false
		case _ = <-p.NodeIsUpChan:
			if p.Parent() != nil {
				p.SendTo(p.Parent(), &NodeIsUp{})
			} else {
				p.Replies++
			}
		case <-time.After(time.Duration(p.Timeout()) * time.Millisecond):
			dbg.Lvl3(p.Info(), "timed out while waiting for", p.Timeout())
			if p.IsRoot() {
				dbg.Lvl2("Didn't get all children in time:", p.Replies)
				p.Count <- p.Replies
				running = false
			}
		}
	}
	p.Done()
	return nil
}

// FuncPC handles PrepareCount messages. These messages go down the tree and
// every node that receives one will reply with a 'NodeIsUp'-message
func (p *ProtocolCount) FuncPC() {
	if !p.IsRoot() {
		p.SendTo(p.Parent(), &NodeIsUp{})
	}
	if !p.IsLeaf() {
		for _, child := range p.Children() {
			go func(c *sda.TreeNode) {
				dbg.Lvl3(p.Info(), "sending to", c.Entity.Addresses, c.Id, p.timeout)
				p.SendTo(c, &PrepareCount{Timeout: p.timeout})
			}(child)
		}
	} else {
		p.CountChan <- nil
	}
}

// FuncC creates a Count-message that will be received by all parents and
// count the total number of children
func (p *ProtocolCount) FuncC(cc []CountMsg) {
	count := 1
	for _, c := range cc {
		count += c.Count.Children
	}
	if !p.IsRoot() {
		dbg.Lvl3(p.Info(), "Sends to", p.Parent().Id, p.Parent().Entity.Addresses)
		p.SendTo(p.Parent(), &Count{count})
	} else {
		p.Count <- count
	}
	dbg.Lvl3(p.Node.Entity().First(), "Done")
}

// SetTimeout sets the new timeout
func (p *ProtocolCount) SetTimeout(t int) {
	p.timeoutMu.Lock()
	p.timeout = t
	p.timeoutMu.Unlock()
}

// Timeout returns the current timeout
func (p *ProtocolCount) Timeout() int {
	p.timeoutMu.Lock()
	defer p.timeoutMu.Unlock()
	return p.timeout
}
