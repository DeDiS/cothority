package sda

import (
	"errors"
	"reflect"

	"fmt"

	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	"github.com/dedis/crypto/abstract"
	"github.com/satori/go.uuid"
)

/*
Node represents a protocol-instance in a given TreeNode. It is linked to
Overlay where all the tree-structures are stored.
*/

type Node struct {
	overlay *Overlay
	token   *Token
	// cache for the TreeNode this Node is representing
	treeNode *TreeNode
	// channels holds all channels available for the different message-types
	channels map[uuid.UUID]interface{}
	// registered handler-functions for that protocol
	handlers map[uuid.UUID]interface{}
	// flags for messages - only one channel/handler possible
	messageTypeFlags map[uuid.UUID]uint32
	// The protocolInstance belonging to that node
	instance ProtocolInstance
	// aggregate messages in order to dispatch them at once in the protocol
	// instance
	msgQueue map[uuid.UUID][]*SDAData
	// done callback
	onDoneCallback func() bool
	// dispatcher of the messages so overlay can give message to node without
	// blocking even if the overlaying protocol is still blocking.
	dispatcher *dispatcher
}

// AggregateMessages (if set) tells to aggregate messages from all children
// before sending to the (parent) Node
// https://golang.org/ref/spec#Iota
const (
	AggregateMessages = 1 << iota
)

// MsgHandler is called upon reception of a certain message-type
type MsgHandler func([]*interface{})

// NewNode creates a new node
func NewNode(o *Overlay, tok *Token) (*Node, error) {
	n, err := NewNodeEmpty(o, tok)
	if err != nil {
		return nil, err
	}
	return n, n.protocolInstantiate()
}

// NewNodeEmpty creates a new node without a protocol
func NewNodeEmpty(o *Overlay, tok *Token) (*Node, error) {
	n := &Node{overlay: o,
		token:            tok,
		channels:         make(map[uuid.UUID]interface{}),
		handlers:         make(map[uuid.UUID]interface{}),
		msgQueue:         make(map[uuid.UUID][]*SDAData),
		messageTypeFlags: make(map[uuid.UUID]uint32),
		treeNode:         nil,
	}
	// creates and runs the dispatcher
	n.dispatcher = newDispatcher(n.dispatchMsgToProtocol)
	go n.dispatcher.run()
	var err error
	n.treeNode, err = n.overlay.TreeNodeFromToken(n.token)
	if err != nil {
		return nil, errors.New("We are not represented in the tree")
	}
	return n, nil
}

// TreeNode gets the treeNode of this node. If there is no TreeNode for the
// Token of this node, the function will return nil
func (n *Node) TreeNode() *TreeNode {
	return n.treeNode
}

// Entity returns our entity
func (n *Node) Entity() *network.Entity {
	return n.treeNode.Entity
}

// Parent returns the parent-TreeNode of ourselves
func (n *Node) Parent() *TreeNode {
	return n.treeNode.Parent
}

// Children returns the children of ourselves
func (n *Node) Children() []*TreeNode {
	return n.treeNode.Children
}

// Root returns the root-node of that tree
func (n *Node) Root() *TreeNode {
	return n.Tree().Root
}

// IsRoot returns whether whether we are at the top of the tree
func (n *Node) IsRoot() bool {
	return n.treeNode.Parent == nil
}

// IsLeaf returns whether whether we are at the bottom of the tree
func (n *Node) IsLeaf() bool {
	return len(n.treeNode.Children) == 0
}

// SendTo sends to a given node
func (n *Node) SendTo(to *TreeNode, msg interface{}) error {
	if to == nil {
		return errors.New("Sent to a nil TreeNode")
	}
	return n.overlay.SendToTreeNode(n.token, to, msg)
}

// Tree returns the tree of that node
func (n *Node) Tree() *Tree {
	return n.overlay.TreeFromToken(n.token)
}

// EntityList returns the entity-list
func (n *Node) EntityList() *EntityList {
	return n.Tree().EntityList
}

func (n *Node) Suite() abstract.Suite {
	return n.overlay.Suite()
}

// RegisterChannel takes a channel with a struct that contains two
// elements: a TreeNode and a message. It will send every message that are the
// same type to this channel.
// This function handles also
// - registration of the message-type
// - aggregation or not of messages: if you give a channel of slices, the
//   messages will be aggregated, else they will come one-by-one
func (n *Node) RegisterChannel(c interface{}) error {
	flags := uint32(0)
	cr := reflect.TypeOf(c)
	if cr.Kind() == reflect.Ptr {
		val := reflect.ValueOf(c).Elem()
		val.Set(reflect.MakeChan(val.Type(), 100))
		//val.Set(reflect.MakeChan(reflect.Indirect(cr), 1))
		return n.RegisterChannel(reflect.Indirect(val).Interface())
	} else if reflect.ValueOf(c).IsNil() {
		return errors.New("Can not Register a (value) channel not initialized")
	}
	// Check we have the correct channel-type
	if cr.Kind() != reflect.Chan {
		return errors.New("Input is not channel")
	}
	if cr.Elem().Kind() == reflect.Slice {
		flags += AggregateMessages
		cr = cr.Elem()
	}
	if cr.Elem().Kind() != reflect.Struct {
		return errors.New("Input is not channel of structure")
	}
	if cr.Elem().NumField() != 2 {
		return errors.New("Input is not channel of structure with 2 elements")
	}
	if cr.Elem().Field(0).Type != reflect.TypeOf(&TreeNode{}) {
		return errors.New("Input-channel doesn't have TreeNode as element")
	}
	// Automatic registration of the message to the network library.
	typ := network.RegisterMessageUUID(network.RTypeToUUID(cr.Elem().Field(1).Type),
		cr.Elem().Field(1).Type)
	n.channels[typ] = c
	//typ := network.RTypeToUUID(cr.Elem().Field(1).Type) n.channels[typ] = c
	n.messageTypeFlags[typ] = flags
	dbg.Lvl4("Registered channel", typ, "with flags", flags)
	return nil
}

// RegisterChannel takes a channel with a struct that contains two
// elements: a TreeNode and a message. It will send every message that are the
// same type to this channel.
// This function handles also
// - registration of the message-type
// - aggregation or not of messages: if you give a channel of slices, the
//   messages will be aggregated, else they will come one-by-one
func (n *Node) RegisterHandler(c interface{}) error {
	flags := uint32(0)
	cr := reflect.TypeOf(c)
	// Check we have the correct channel-type
	if cr.Kind() != reflect.Func {
		return errors.New("Input is not function")
	}
	cr = cr.In(0)
	if cr.Kind() == reflect.Slice {
		flags += AggregateMessages
		cr = cr.Elem()
	}
	if cr.Kind() != reflect.Struct {
		return errors.New("Input is not channel of structure")
	}
	if cr.NumField() != 2 {
		return errors.New("Input is not channel of structure with 2 elements")
	}
	if cr.Field(0).Type != reflect.TypeOf(&TreeNode{}) {
		return errors.New("Input-channel doesn't have TreeNode as element")
	}
	// Automatic registration of the message to the network library.
	typ := network.RegisterMessageUUID(network.RTypeToUUID(cr.Field(1).Type),
		cr.Field(1).Type)
	//typ := network.RTypeToUUID(cr.Elem().Field(1).Type)
	n.handlers[typ] = c
	n.messageTypeFlags[typ] = flags
	dbg.Lvl3("Registered handler", typ, "with flags", flags)
	return nil
}

// ProtocolInstance returns the instance of the running protocol
func (n *Node) ProtocolInstance() ProtocolInstance {
	return n.instance
}

// ProtocolInstantiate creates a new instance of a protocol given by it's name
func (n *Node) protocolInstantiate() error {
	if n.token == nil {
		return errors.New("Hope this is running in test-mode")
	}
	pid := n.token.ProtocolID
	p, ok := protocols[pid]
	if !ok {
		return errors.New("Protocol " + pid.String() + " doesn't exist")
	}
	tree := n.overlay.Tree(n.token.TreeID)
	if tree == nil {
		return errors.New("Tree does not exists")
	}
	if n.overlay.EntityList(n.token.EntityListID) == nil {
		return errors.New("EntityList does not exists")
	}

	var err error
	n.instance, err = p(n)
	go n.instance.Dispatch()
	return err
}

// Dispatch - the standard dispatching function is empty
func (n *Node) Dispatch() error {
	return nil
}

// Shutdown - standard Shutdown implementation. Define your own
// in your protocol (if necessary)
func (n *Node) Shutdown() error {
	return nil
}

func (n *Node) DispatchHandler(msgSlice []*SDAData) error {
	mt := msgSlice[0].MsgType
	to := reflect.TypeOf(n.handlers[mt]).In(0)
	f := reflect.ValueOf(n.handlers[mt])
	if n.HasFlag(mt, AggregateMessages) {
		msgs := reflect.MakeSlice(to, len(msgSlice), len(msgSlice))
		for i, msg := range msgSlice {
			msgs.Index(i).Set(n.ReflectCreate(to.Elem(), msg))
		}
		dbg.Lvl4("Dispatching aggregation to", n.Entity().Addresses)
		f.Call([]reflect.Value{msgs})
	} else {
		for _, msg := range msgSlice {
			dbg.Lvl4("Dispatching to", n.Entity().Addresses)
			m := n.ReflectCreate(to, msg)
			f.Call([]reflect.Value{m})
		}
	}
	return nil
}

func (n *Node) ReflectCreate(t reflect.Type, msg *SDAData) reflect.Value {
	m := reflect.Indirect(reflect.New(t))
	tn := n.Tree().GetTreeNode(msg.From.TreeNodeID)
	if tn != nil {
		m.Field(0).Set(reflect.ValueOf(tn))
		m.Field(1).Set(reflect.Indirect(reflect.ValueOf(msg.Msg)))
	}
	return m
}

// DispatchChannel takes a message and sends it to a channel
func (n *Node) DispatchChannel(msgSlice []*SDAData) error {
	mt := msgSlice[0].MsgType
	to := reflect.TypeOf(n.channels[mt])
	if n.HasFlag(mt, AggregateMessages) {
		dbg.Lvl4("Received aggregated message of type:", mt)
		to = to.Elem()
		out := reflect.MakeSlice(to, len(msgSlice), len(msgSlice))
		for i, msg := range msgSlice {
			dbg.Lvl4("Dispatching aggregated to", to)
			m := n.ReflectCreate(to.Elem(), msg)
			dbg.Lvl4("Adding msg", m, "to", n.Entity().Addresses)
			out.Index(i).Set(m)
		}
		reflect.ValueOf(n.channels[mt]).Send(out)
	} else {
		for _, msg := range msgSlice {
			out := n.channels[mt]

			m := n.ReflectCreate(to.Elem(), msg)
			dbg.Lvl4("Dispatching msg type", mt, " to", to, " :", m.Field(1).Interface())
			reflect.ValueOf(out).Send(m)
		}
	}
	return nil
}

// Dispatchmsg is the public version used by Overlay to dispatch a message to
// the protocol instance using the dispatcher.It is a non blocking operation.
func (n *Node) DispatchMsg(sdaMsg *SDAData) {
	n.dispatcher.input <- sdaMsg
}

// DispatchMsg will dispatch this SDAData to the protocol instance.
func (n *Node) dispatchMsgToProtocol(sdaMsg *SDAData) {
	// Decode the inner message here. In older versions, it was decoded before,
	// but first there is no use to do it before, and then every protocols had
	// to manually registers their messages. Since it is done automatically by
	// the Node, decoding should also be done by the node.
	var err error
	t, msg, err := network.UnmarshalRegisteredType(sdaMsg.MsgSlice, network.DefaultConstructors(n.Suite()))
	if err != nil {
		dbg.Error(n.Entity().First(), "Error while unmarshalling inner message of SDAData", sdaMsg.MsgType, ":", err)
	}
	// Put the msg into SDAData
	sdaMsg.MsgType = t
	sdaMsg.Msg = msg
	dbg.Lvlf5("SDA-Message is: %+v", sdaMsg.Msg)

	// if message comes from parent, dispatch directly
	// if messages come from children we must aggregate them
	// if we still need to wait for additional messages, we return
	msgType, msgs, done := n.aggregate(sdaMsg)
	if !done {
		dbg.Lvl3(n.Name(), "Not done aggregating children msgs")
		return
	}
	dbg.Lvl4("Going to dispatch", sdaMsg)

	switch {
	case n.channels[msgType] != nil:
		dbg.Lvl4("Dispatching to channel")
		err = n.DispatchChannel(msgs)
	case n.handlers[msgType] != nil:
		dbg.Lvl4("Dispatching to handler", n.Entity().Addresses)
		err = n.DispatchHandler(msgs)
	default:
		err = errors.New("This message-type is not handled by this protocol")
	}
	// XXX Should at some point define a central way / point to handle errors
	// ... ?
	// Before the error was just going up to the sda.Host and nothing else was
	// taken into account.
	if err != nil {
		dbg.Error(err)
	}
}

// SetFlag makes sure a given flag is set
func (n *Node) SetFlag(mt uuid.UUID, f uint32) {
	n.messageTypeFlags[mt] |= f
}

// ClearFlag makes sure a given flag is removed
func (n *Node) ClearFlag(mt uuid.UUID, f uint32) {
	n.messageTypeFlags[mt] &^= f
}

// HasFlag returns true if the given flag is set
func (n *Node) HasFlag(mt uuid.UUID, f uint32) bool {
	return n.messageTypeFlags[mt]&f != 0
}

// aggregate store the message for a protocol instance such that a protocol
// instances will get all its children messages at once.
// node is the node the host is representing in this Tree, and sda is the
// message being analyzed.
func (n *Node) aggregate(sdaMsg *SDAData) (uuid.UUID, []*SDAData, bool) {
	mt := sdaMsg.MsgType
	fromParent := !n.IsRoot() && uuid.Equal(sdaMsg.From.TreeNodeID, n.Parent().Id)
	if fromParent || !n.HasFlag(mt, AggregateMessages) {
		return mt, []*SDAData{sdaMsg}, true
	}
	// store the msg according to its type
	if _, ok := n.msgQueue[mt]; !ok {
		n.msgQueue[mt] = make([]*SDAData, 0)
	}
	msgs := append(n.msgQueue[mt], sdaMsg)
	n.msgQueue[mt] = msgs
	dbg.Lvl4(n.Entity().Addresses, "received", len(msgs), "of", len(n.Children()), "messages")

	// do we have everything yet or no
	// get the node this host is in this tree
	// OK we have all the children messages
	if len(msgs) == len(n.Children()) {
		// erase
		delete(n.msgQueue, mt)
		return mt, msgs, true
	}
	// no we still have to wait!
	return mt, nil, false
}

// Start calls the start-method on the protocol which in turn will initiate
// the first message to its children
func (n *Node) Start() error {
	return n.instance.Start()
}

// Done returns a channel that must be given a bool when a protocol instance has
// finished its work.
func (n *Node) Done() {
	if n.onDoneCallback != nil {
		ok := n.onDoneCallback()
		if !ok {
			return
		}
	}
	n.overlay.nodeDone(n.token)
	n.dispatcher.stop()
	dbg.Lvl3(n.Name(), "has finished. Deleting its resources")
}

// OnDoneCallback should be called if we want to control the Done() of the node.
// It is used by protocols that uses others protocols inside and that want to
// control when the final Done() should be called.
// the function should return true if the real Done() has to be called otherwise
// false.
func (n *Node) OnDoneCallback(fn func() bool) {
	n.onDoneCallback = fn
}

// Private returns the corresponding private key
func (n *Node) Private() abstract.Secret {
	return n.overlay.host.private
}

// Public() returns the public key.
func (n *Node) Public() abstract.Point {
	return n.Entity().Public
}

// CloseHost closes the underlying sda.Host (which closes the overlay
// and sends Shutdown to all protocol instances)
func (n *Node) CloseHost() error {
	return n.Host().Close()
}

func (n *Node) Name() string {
	return n.Entity().First()
}

func (n *Node) TokenID() uuid.UUID {
	return n.token.Id()
}

func (n *Node) Token() *Token {
	return n.token
}

// Myself nicely displays who we are
func (n *Node) Myself() string {
	return fmt.Sprint(n.Entity().Addresses, n.TokenID())
}

// Host returns the underlying Host of this node.
// WARNING: you should not play with that feature unless you know what you are
// doing. This feature is mean to access the low level parts of the API. For
// example it is used to add a new tree config / new entity list to the host.
func (n *Node) Host() *Host {
	return n.overlay.host
}

// SetProtocolInstance is used when you first create an empty node and you want
// to bind it to a protocol instance later.
func (n *Node) SetProtocolInstance(pi ProtocolInstance) {
	n.instance = pi
}

// a type to represent a channel of sdadata messages
type sdaDataChan chan *SDAData

// a type to represent the method to use to really dispatch the method
type dispatchMethod func(msg *SDAData)

// dispatcher is the struct responsible to dispatch the message to the protocol
// instance in a non blocking way for SDA /host/ so multiple messages can still
// arrive even if the protocol is *blocked*.
type dispatcher struct {
	// input channel  - the message to dispatch
	input sdaDataChan
	// quit channel signal
	// signalQuitChan for signaling we want to stop
	// getQuitChan to wait on until the dispatcher really stoped
	signalQuitChan chan bool
	getQuitChan    chan bool
	// the set of workers this dispatcher has. Basically for node it supposed to
	// only have one.
	worker *worker
	// channel of the worker to signify if it is ready or not
	// once the dispatcher receive stg on this channel it can sends some
	// ready-to-be-dispatched message on the worker channel
	// simplification of a generalization where you can have N workers, in this
	// case you would use `chan sdaDataChan`
	readyChan chan bool
}

// newDispatcher returns a new dispatcher out of the input channel and the
// actual method that makes the dispatching (passing to the p.i.)
func newDispatcher(dispatch dispatchMethod) *dispatcher {
	d := &dispatcher{
		input:          make(sdaDataChan),
		readyChan:      make(chan bool, 1),
		signalQuitChan: make(chan bool),
		getQuitChan:    make(chan bool),
	}
	d.worker = newWorker(d.readyChan, dispatch)
	return d
}

// worker is the struct that sends the message to the protocol instance. If the
// protocol instance is blocked, then the worker is also blocked. Once the
// message has been given to the p.i., it signal itself again to the dispatcher.
type worker struct {
	// the general channel used by this worker to signify that it is
	// ready ==> readyChan from the dispatcher
	readyChan chan bool
	// msgChan is the channel used by the dispatcher to give the message to the
	// worker
	msgChan sdaDataChan
	// the actual method to call to dispatch the msg
	dispatch dispatchMethod
	// quitChan is used to stop the worker
	quitChan chan bool
}

// newWorker returns a new worker out of
// * readyChan so the worker signal the dispatcher that it is ready
// * msgChan so the worker know where to listen for msgs
// * and the dispatch method.
func newWorker(readyChan chan bool, dispatch dispatchMethod) *worker {
	return &worker{
		dispatch:  dispatch,
		msgChan:   make(sdaDataChan),
		readyChan: readyChan,
		quitChan:  make(chan bool),
	}
}

func (d *dispatcher) stop() {
	d.signalQuitChan <- true
	<-d.getQuitChan
}

// run waits for the input messages and make the dispatching happens
func (d *dispatcher) run() {
	// run the worker first
	go d.worker.run()
	// the buffer that holds the message while we wait for worker to be ready
	// A capacity of 10 messages first seems reasonable. If more than 10 msgs are blocked then
	// append() will grow one by one and it may be slower but at some point if
	// it continues to block something is wrong, and a unlimited memory machine
	// does not exists yet ;)
	buff := make([]*SDAData, 0, 10)
	var workerReady bool
	for {
		select {
		case msg := <-d.input:
			// XXX Put a limit on the buffer regarding the maximum memory we
			// have and the available memory ... or just a high enough constant.
			// And what to do with the message ?
			// we get a new message to dispatch
			buff = append(buff, msg)
			if len(buff) == 1 && workerReady {
				d.worker.msg(buff[0])
				buff = buff[1:]
				workerReady = false
			}
		case <-d.readyChan:
			// the worker is ready
			if len(buff) > 0 {
				d.worker.msg(buff[0])
				buff = buff[1:]
			} else {
				// put the worker into *ready* state
				workerReady = true
			}
		case <-d.signalQuitChan:
			// we are closing down so we can tell the worker to finish
			// graceful shutdown
			d.worker.stop()
			close(d.signalQuitChan)
			close(d.input)
			close(d.readyChan)
			d.getQuitChan <- true
			return
		}
	}
}

// run launches the goroutine for the worker so it gets all messages that the
// dispatcher is willing to send to it.
func (w *worker) run() {
	var stop bool
	for !stop {
		w.readyChan <- true
		select {
		case msg := <-w.msgChan:
			// this is the blocking function
			w.dispatch(msg)
		case <-w.quitChan:
			stop = true
		}
	}
	close(w.quitChan)
}

// msg will give the message to the worker
func (w *worker) msg(msg *SDAData) {
	w.msgChan <- msg
}

// stop will tell the worker to stop
func (w *worker) stop() {
	w.quitChan <- true
}
