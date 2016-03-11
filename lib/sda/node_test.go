package sda_test

import (
	"testing"
	"time"

	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	"github.com/dedis/cothority/lib/sda"
	"github.com/dedis/cothority/protocols/manage"
	"github.com/satori/go.uuid"
)

func init() {
	sda.ProtocolRegisterName("ProtocolChannels", NewProtocolChannels)
	sda.ProtocolRegisterName("ProtocolHandlers", NewProtocolHandlers)
	sda.ProtocolRegister(testID, NewProtocolTest)
	Incoming = make(chan struct {
		*sda.TreeNode
		NodeTestMsg
	})
}

func TestNodeChannelCreateSlice(t *testing.T) {
	defer dbg.AfterTest(t)
	dbg.TestOutput(testing.Verbose(), 4)
	local := sda.NewLocalTest()
	_, _, tree := local.GenTree(2, false, true, true)
	defer local.CloseAll()

	n, err := local.NewNode(tree.Root, "ProtocolChannels")
	if err != nil {
		t.Fatal("Couldn't create new node:", err)
	}

	var c chan []struct {
		*sda.TreeNode
		NodeTestMsg
	}
	err = n.RegisterChannel(&c)
	if err != nil {
		t.Fatal("Couldn't register channel:", err)
	}
}

func TestNodeChannelCreate(t *testing.T) {
	defer dbg.AfterTest(t)

	dbg.TestOutput(testing.Verbose(), 4)

	local := sda.NewLocalTest()
	_, _, tree := local.GenTree(2, false, true, true)
	defer local.CloseAll()

	n, err := local.NewNode(tree.Root, "ProtocolChannels")
	if err != nil {
		t.Fatal("Couldn't create new node:", err)
	}
	var c chan struct {
		*sda.TreeNode
		NodeTestMsg
	}
	err = n.RegisterChannel(&c)
	if err != nil {
		t.Fatal("Couldn't register channel:", err)
	}
	err = n.DispatchChannel([]*sda.SDAData{&sda.SDAData{
		Msg:     NodeTestMsg{3},
		MsgType: network.RegisterMessageType(NodeTestMsg{}),
		From: &sda.Token{
			TreeID:     tree.Id,
			TreeNodeID: tree.Root.Id,
		}},
	})
	if err != nil {
		t.Fatal("Couldn't dispatch to channel:", err)
	}
	msg := <-c
	if msg.I != 3 {
		t.Fatal("Message should contain '3'")
	}
}

func TestNodeChannel(t *testing.T) {
	defer dbg.AfterTest(t)

	dbg.TestOutput(testing.Verbose(), 4)

	local := sda.NewLocalTest()
	_, _, tree := local.GenTree(2, false, true, true)
	defer local.CloseAll()

	n, err := local.NewNode(tree.Root, "ProtocolChannels")
	if err != nil {
		t.Fatal("Couldn't create new node:", err)
	}
	c := make(chan struct {
		*sda.TreeNode
		NodeTestMsg
	}, 1)
	err = n.RegisterChannel(c)
	if err != nil {
		t.Fatal("Couldn't register channel:", err)
	}
	err = n.DispatchChannel([]*sda.SDAData{&sda.SDAData{
		Msg:     NodeTestMsg{3},
		MsgType: network.RegisterMessageType(NodeTestMsg{}),
		From: &sda.Token{
			TreeID:     tree.Id,
			TreeNodeID: tree.Root.Id,
		}},
	})
	if err != nil {
		t.Fatal("Couldn't dispatch to channel:", err)
	}
	msg := <-c
	if msg.I != 3 {
		t.Fatal("Message should contain '3'")
	}
}

// Test instantiation of Node
func TestNewNode(t *testing.T) {
	defer dbg.AfterTest(t)

	h1, h2 := SetupTwoHosts(t, false)
	// Add tree + entitylist
	el := sda.NewEntityList([]*network.Entity{h1.Entity, h2.Entity})
	h1.AddEntityList(el)
	tree := el.GenerateBinaryTree()
	h1.AddTree(tree)

	// Try directly StartNewNode
	node, err := h1.StartNewNode(testID, tree)
	if err != nil {
		t.Fatal("Could not start new protocol", err)
	}
	p := node.ProtocolInstance().(*ProtocolTest)
	m := <-p.DispMsg
	if m != "Dispatch" {
		t.Fatal("Dispatch() not called - msg is:", m)
	}
	m = <-p.StartMsg
	if m != "Start" {
		t.Fatal("Start() not called - msg is:", m)
	}
	h1.Close()
	h2.Close()
}

func TestProtocolChannels(t *testing.T) {
	defer dbg.AfterTest(t)

	dbg.TestOutput(testing.Verbose(), 4)
	h1, h2 := SetupTwoHosts(t, true)
	defer h1.Close()
	defer h2.Close()
	// Add tree + entitylist
	el := sda.NewEntityList([]*network.Entity{h1.Entity, h2.Entity})
	h1.AddEntityList(el)
	tree := el.GenerateBinaryTree()
	h1.AddTree(tree)
	h1.StartProcessMessages()

	// Try directly StartNewProtocol
	_, err := h1.StartNewNodeName("ProtocolChannels", tree)
	if err != nil {
		t.Fatal("Couldn't start protocol:", err)
	}

	select {
	case msg := <-Incoming:
		if msg.I != 12 {
			t.Fatal("Child should receive 12")
		}
	case <-time.After(time.Second * 3):
		t.Fatal("Timeout")
	}
}

func TestProtocolHandlers(t *testing.T) {
	defer dbg.AfterTest(t)

	local := sda.NewLocalTest()
	_, _, tree := local.GenTree(3, false, true, true)
	defer local.CloseAll()
	dbg.Lvl2("Sending to children")
	IncomingHandlers = make(chan *sda.Node, 2)
	node, err := local.StartNewNodeName("ProtocolHandlers", tree)
	if err != nil {
		t.Fatal(err)
	}
	dbg.Lvl2("Waiting for responses")
	child1 := <-IncomingHandlers
	child2 := <-IncomingHandlers

	if child1.Entity().Id == child2.Entity().Id {
		t.Fatal("Both entities should be different")
	}

	dbg.Lvl2("Sending to parent")

	child1.SendTo(node.TreeNode(), &NodeTestAggMsg{})
	if len(IncomingHandlers) > 0 {
		t.Fatal("This should not trigger yet")
	}
	child2.SendTo(node.TreeNode(), &NodeTestAggMsg{})
	final := <-IncomingHandlers
	if final.Entity().Id != node.Entity().Id {
		t.Fatal("This should be the same ID")
	}
}

func TestMsgAggregation(t *testing.T) {
	defer dbg.AfterTest(t)

	local := sda.NewLocalTest()
	_, _, tree := local.GenTree(3, false, true, true)
	defer local.CloseAll()
	root, err := local.StartNewNodeName("ProtocolChannels", tree)
	if err != nil {
		t.Fatal("Couldn't create new node:", err)
	}
	proto := root.ProtocolInstance().(*ProtocolChannels)
	// Wait for both children to be up
	<-Incoming
	<-Incoming
	dbg.Lvl3("Both children are up")
	child1 := local.GetNodes(tree.Root.Children[0])[0]
	child2 := local.GetNodes(tree.Root.Children[1])[0]

	err = local.SendTreeNode("ProtocolChannels", child1, root, &NodeTestAggMsg{3})
	if err != nil {
		t.Fatal(err)
	}
	if len(proto.IncomingAgg) > 0 {
		t.Fatal("Messages should NOT be there")
	}
	err = local.SendTreeNode("ProtocolChannels", child2, root, &NodeTestAggMsg{4})
	if err != nil {
		t.Fatal(err)
	}
	if len(proto.IncomingAgg) == 0 {
		t.Fatal("Messages should BE there")
	}
	msgs := <-proto.IncomingAgg
	if msgs[0].I != 3 {
		t.Fatal("First message should be 3")
	}
	if msgs[1].I != 4 {
		t.Fatal("Second message should be 4")
	}
}

func TestFlags(t *testing.T) {
	defer dbg.AfterTest(t)

	local := sda.NewLocalTest()
	_, _, tree := local.GenTree(3, false, false, true)
	defer local.CloseAll()
	n, err := local.NewNode(tree.Root, "ProtocolChannels")
	if err != nil {
		t.Fatal("Couldn't create node.")
	}
	if n.HasFlag(uuid.Nil, sda.AggregateMessages) {
		t.Fatal("Should NOT have AggregateMessages-flag")
	}
	n.SetFlag(uuid.Nil, sda.AggregateMessages)
	if !n.HasFlag(uuid.Nil, sda.AggregateMessages) {
		t.Fatal("Should HAVE AggregateMessages-flag cleared")
	}
	n.ClearFlag(uuid.Nil, sda.AggregateMessages)
	if n.HasFlag(uuid.Nil, sda.AggregateMessages) {
		t.Fatal("Should NOT have AggregateMessages-flag")
	}
}

func TestSendLimitedTree(t *testing.T) {
	defer dbg.AfterTest(t)

	local := sda.NewLocalTest()
	_, _, tree := local.GenBigTree(7, 1, 2, true, true)
	defer local.CloseAll()

	dbg.Lvl3(tree.Dump())

	root, err := local.StartNewNodeName("Count", tree)
	if err != nil {
		t.Fatal("Couldn't create new node:", err)
	}
	protoCount := root.ProtocolInstance().(*manage.ProtocolCount)
	count := <-protoCount.Count
	if count != 7 {
		t.Fatal("Didn't get a count of 7:", count)
	}
}

type NodeTestMsg struct {
	I int
}

var Incoming chan struct {
	*sda.TreeNode
	NodeTestMsg
}

type NodeTestAggMsg struct {
	I int
}

type ProtocolChannels struct {
	*sda.Node
	IncomingAgg chan []struct {
		*sda.TreeNode
		NodeTestAggMsg
	}
}

func NewProtocolChannels(n *sda.Node) (sda.ProtocolInstance, error) {
	p := &ProtocolChannels{
		Node: n,
	}
	p.RegisterChannel(Incoming)
	p.RegisterChannel(&p.IncomingAgg)
	return p, nil
}

func (p *ProtocolChannels) Start() error {
	for _, c := range p.Children() {
		err := p.SendTo(c, &NodeTestMsg{12})
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *ProtocolChannels) Dispatch() error {
	return nil
}

// relese ressources ==> call Done()
func (p *ProtocolChannels) Release() {
	p.Done()
}

type ProtocolHandlers struct {
	*sda.Node
}

var IncomingHandlers chan *sda.Node

func NewProtocolHandlers(n *sda.Node) (sda.ProtocolInstance, error) {
	p := &ProtocolHandlers{
		Node: n,
	}
	p.RegisterHandler(p.HandleMessageOne)
	p.RegisterHandler(p.HandleMessageAggregate)
	return p, nil
}

func (p *ProtocolHandlers) Start() error {
	for _, c := range p.Children() {
		err := p.SendTo(c, &NodeTestMsg{12})
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *ProtocolHandlers) HandleMessageOne(msg struct {
	*sda.TreeNode
	NodeTestMsg
}) {
	IncomingHandlers <- p.Node
}

func (p *ProtocolHandlers) HandleMessageAggregate(msg []struct {
	*sda.TreeNode
	NodeTestAggMsg
}) {
	dbg.Lvl3("Received message")
	IncomingHandlers <- p.Node
}

func (p *ProtocolHandlers) Dispatch() error {
	return nil
}

// relese ressources ==> call Done()
func (p *ProtocolHandlers) Release() {
	p.Done()
}
