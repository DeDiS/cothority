package sda

import (
	"testing"
	"time"

	"sync"

	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/network"
	"github.com/dedis/protobuf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dummyServiceName = "dummyService"
const ismServiceName = "ismService"
const backForthServiceName = "backForth"

func init() {
	network.RegisterPacketType(SimpleMessageForth{})
	network.RegisterPacketType(SimpleMessageBack{})
	network.RegisterPacketType(SimpleRequest{})
	dummyMsgType = network.RegisterPacketType(DummyMsg{})
	RegisterNewService(ismServiceName, newServiceMessages)
}

func TestServiceRegistration(t *testing.T) {
	var name = "dummy"
	RegisterNewService(name, func(c *Context, path string) Service {
		return &DummyService{}
	})

	names := ServiceFactory.RegisteredServiceNames()
	var found bool
	for _, n := range names {
		if n == name {
			found = true
		}
	}
	if !found {
		t.Fatal("Name not found !?")
	}
	ServiceFactory.Unregister(name)
	names = ServiceFactory.RegisteredServiceNames()
	for _, n := range names {
		if n == name {
			t.Fatal("Dummy should not be found!")
		}
	}
}

func TestServiceNew(t *testing.T) {
	ds := &DummyService{
		link: make(chan bool),
	}
	RegisterNewService(dummyServiceName, func(c *Context, path string) Service {
		ds.c = c
		ds.path = path
		ds.link <- true
		return ds
	})
	defer UnregisterService(dummyServiceName)
	go func() {
		local := NewLocalTest()
		local.GenConodes(1)
		defer local.CloseAll()
	}()

	waitOrFatal(ds.link, t)
}

func TestServiceProcessRequest(t *testing.T) {
	link := make(chan bool, 1)
	log.ErrFatal(RegisterNewService(dummyServiceName, func(c *Context, path string) Service {
		ds := &DummyService{
			link: link,
			c:    c,
			path: path,
		}
		return ds
	}))
	defer UnregisterService(dummyServiceName)

	local := NewTCPTest()
	hs := local.GenConodes(2)
	conode := hs[0]
	log.Lvl1("Host created and listening")
	defer local.CloseAll()
	// Send a request to the service
	client := NewClient(dummyServiceName)
	log.Lvl1("Sending request to service...")
	_, err := client.Send(conode.ServerIdentity, "nil", []byte("a"))
	log.Lvl2("Got reply")
	require.Error(t, err)
	require.Equal(t, 4100, err.ErrorCode())
	require.Equal(t, "wrong message", err.ErrorMsg())
	// wait for the link
	if <-link {
		t.Fatal("was expecting false !")
	}
}

// Test if a request that makes the service create a new protocol works
func TestServiceRequestNewProtocol(t *testing.T) {
	ds := &DummyService{
		link: make(chan bool, 1),
	}
	RegisterNewService(dummyServiceName, func(c *Context, path string) Service {
		ds.c = c
		ds.path = path
		return ds
	})

	defer UnregisterService(dummyServiceName)
	local := NewTCPTest()
	hs := local.GenConodes(2)
	conode := hs[0]
	client := local.NewClient(dummyServiceName)
	defer local.CloseAll()
	// create the entityList and tree
	el := NewRoster([]*network.ServerIdentity{conode.ServerIdentity})
	tree := el.GenerateBinaryTree()
	// give it to the service
	ds.fakeTree = tree

	// Send a request to the service
	log.Lvl1("Sending request to service...")
	log.ErrFatal(client.SendProtobuf(conode.ServerIdentity, &DummyMsg{10}, nil))
	// wait for the link from the
	waitOrFatalValue(ds.link, true, t)

	// Now resend the value so we instantiate using the same treenode
	log.Lvl1("Sending request again to service...")
	cerr := client.SendProtobuf(conode.ServerIdentity, &DummyMsg{10}, nil)
	assert.Error(t, cerr)
	// this should fail
	waitOrFatalValue(ds.link, false, t)
}

// test for calling the NewProtocol method on a remote Service
func TestServiceNewProtocol(t *testing.T) {
	ds1 := &DummyService{
		link: make(chan bool),
		Config: DummyConfig{
			Send: true,
		},
	}
	ds2 := &DummyService{
		link: make(chan bool),
	}
	var count int
	countMutex := sync.Mutex{}
	RegisterNewService(dummyServiceName, func(c *Context, path string) Service {
		countMutex.Lock()
		defer countMutex.Unlock()
		log.Lvl2("Creating service", count)
		var localDs *DummyService
		switch count {
		case 2:
			// the client does not need a Service
			return &DummyService{link: make(chan bool)}
		case 1: // children
			localDs = ds2
		case 0: // root
			localDs = ds1
		}
		localDs.c = c
		localDs.path = path

		count++
		return localDs
	})

	defer UnregisterService(dummyServiceName)
	local := NewTCPTest()
	defer local.CloseAll()
	hs := local.GenConodes(3)
	conode1, conode2 := hs[0], hs[1]
	client := local.NewClient(dummyServiceName)
	log.Lvl1("Host created and listening")

	// create the entityList and tree
	el := NewRoster([]*network.ServerIdentity{conode1.ServerIdentity, conode2.ServerIdentity})
	tree := el.GenerateBinaryTree()
	// give it to the service
	ds1.fakeTree = tree

	// Send a request to the service
	log.Lvl1("Sending request to service...")
	log.ErrFatal(client.SendProtobuf(conode1.ServerIdentity, &DummyMsg{10}, nil))
	log.Lvl1("Waiting for end")
	// wait for the link from the protocol that Starts
	waitOrFatalValue(ds1.link, true, t)
	// now wait for the second link on the second HOST that the second service
	// should have started (ds2) in ProcessRequest
	waitOrFatalValue(ds2.link, true, t)
	log.Lvl1("Done")
}

func TestServiceProcessor(t *testing.T) {
	ds1 := &DummyService{
		link: make(chan bool),
	}
	ds2 := &DummyService{
		link: make(chan bool),
	}
	var count int
	RegisterNewService(dummyServiceName, func(c *Context, path string) Service {
		var s *DummyService
		if count == 0 {
			s = ds1
		} else {
			s = ds2
		}
		s.c = c
		s.path = path
		c.RegisterProcessor(s, dummyMsgType)
		return s
	})
	local := NewLocalTest()
	defer local.CloseAll()
	hs := local.GenConodes(2)
	conode1, conode2 := hs[0], hs[1]

	defer UnregisterService(dummyServiceName)
	// create two conodes
	log.Lvl1("Host created and listening")
	// create request
	log.Lvl1("Sending request to service...")
	assert.Nil(t, conode2.Send(conode1.ServerIdentity, &DummyMsg{10}))

	// wait for the link from the Service on conode 1
	waitOrFatalValue(ds1.link, true, t)
}

func TestServiceBackForthProtocol(t *testing.T) {
	local := NewTCPTest()
	defer local.CloseAll()

	// register service
	log.ErrFatal(RegisterNewService(backForthServiceName, func(c *Context, path string) Service {
		return &simpleService{
			ctx: c,
		}
	}))
	defer ServiceFactory.Unregister(backForthServiceName)

	// create conodes
	conodes, el, _ := local.GenTree(4, false)

	// create client
	client := local.NewClient(backForthServiceName)

	// create request
	r := &SimpleRequest{
		ServerIdentities: el,
		Val:              10,
	}
	sr := &SimpleResponse{}
	err := client.SendProtobuf(conodes[0].ServerIdentity, r, sr)
	log.ErrFatal(err)
	assert.Equal(t, sr.Val, 10)
}

func TestServiceManager_Service(t *testing.T) {
	local := NewLocalTest()
	defer local.CloseAll()
	conodes, _, _ := local.GenTree(2, true)

	services := conodes[0].serviceManager.AvailableServices()
	assert.NotEqual(t, 0, len(services), "no services available")

	service := conodes[0].serviceManager.Service("testService")
	assert.NotNil(t, service, "Didn't find service testService")
}

func TestServiceMessages(t *testing.T) {
	local := NewLocalTest()
	defer local.CloseAll()
	conodes, _, _ := local.GenTree(2, true)

	service := conodes[0].serviceManager.Service(ismServiceName)
	assert.NotNil(t, service, "Didn't find service ISMService")
	ism := service.(*ServiceMessages)
	ism.SendRaw(conodes[0].ServerIdentity, &SimpleResponse{})
	require.True(t, <-ism.GotResponse, "Didn't get response")
}

// BackForthProtocolForth & Back are messages that go down and up the tree.
// => BackForthProtocol protocol / message
type SimpleMessageForth struct {
	Val int
}

type SimpleMessageBack struct {
	Val int
}

type BackForthProtocol struct {
	*TreeNodeInstance
	Val       int
	counter   int
	forthChan chan struct {
		*TreeNode
		SimpleMessageForth
	}
	backChan chan struct {
		*TreeNode
		SimpleMessageBack
	}
	handler func(val int)
}

func newBackForthProtocolRoot(tn *TreeNodeInstance, val int, handler func(int)) (ProtocolInstance, error) {
	s, err := newBackForthProtocol(tn)
	s.Val = val
	s.handler = handler
	return s, err
}

func newBackForthProtocol(tn *TreeNodeInstance) (*BackForthProtocol, error) {
	s := &BackForthProtocol{
		TreeNodeInstance: tn,
	}
	err := s.RegisterChannel(&s.forthChan)
	if err != nil {
		return nil, err
	}
	err = s.RegisterChannel(&s.backChan)
	if err != nil {
		return nil, err
	}
	go s.dispatch()
	return s, nil
}

func (sp *BackForthProtocol) Start() error {
	// send down to children
	msg := &SimpleMessageForth{
		Val: sp.Val,
	}
	for _, ch := range sp.Children() {
		if err := sp.SendTo(ch, msg); err != nil {
			return err
		}
	}
	return nil
}

func (sp *BackForthProtocol) dispatch() {
	for {
		select {
		// dispatch the first msg down
		case m := <-sp.forthChan:
			msg := &m.SimpleMessageForth
			for _, ch := range sp.Children() {
				sp.SendTo(ch, msg)
			}
			if sp.IsLeaf() {
				if err := sp.SendTo(sp.Parent(), &SimpleMessageBack{msg.Val}); err != nil {
					log.Error(err)
				}
				sp.Done()
				return
			}
		// pass the message up
		case m := <-sp.backChan:
			msg := m.SimpleMessageBack
			// call the handler  if we are the root
			sp.counter++
			if sp.counter == len(sp.Children()) {
				if sp.IsRoot() {
					sp.handler(msg.Val)
				} else {
					sp.SendTo(sp.Parent(), &msg)
				}
				sp.Done()
				return
			}
		}
	}
}

// Client API request / response emulation
type SimpleRequest struct {
	ServerIdentities *Roster
	Val              int
}

type SimpleResponse struct {
	Val int
}

var SimpleResponseType = network.RegisterPacketType(SimpleResponse{})

type simpleService struct {
	ctx *Context
}

func (s *simpleService) ProcessClientRequest(path string, buf []byte) ([]byte, ClientError) {
	msg := &SimpleRequest{}
	err := protobuf.DecodeWithConstructors(buf, msg, network.DefaultConstructors(network.Suite))
	if err != nil {
		return nil, NewClientErrorCode(WebSocketErrorProtobufDecode, "")
	}
	tree := msg.ServerIdentities.GenerateBinaryTree()
	tni := s.ctx.NewTreeNodeInstance(tree, tree.Root, backForthServiceName)
	ret := make(chan int)
	proto, err := newBackForthProtocolRoot(tni, msg.Val, func(n int) {
		ret <- n
	})
	if err != nil {
		return nil, NewClientErrorCode(4100, "")
	}
	if err := s.ctx.RegisterProtocolInstance(proto); err != nil {
		return nil, NewClientErrorCode(4101, "")
	}
	proto.Start()
	resp, err := protobuf.Encode(&SimpleResponse{<-ret})
	if err != nil {
		return nil, NewClientErrorCode(4102, "")
	}
	return resp, nil
}

func (s *simpleService) NewProtocol(tni *TreeNodeInstance, conf *GenericConfig) (ProtocolInstance, error) {
	pi, err := newBackForthProtocol(tni)
	return pi, err
}

func (s *simpleService) Process(packet *network.Packet) {
	return
}

type DummyProtocol struct {
	*TreeNodeInstance
	link   chan bool
	config DummyConfig
}

type DummyConfig struct {
	A    int
	Send bool
}

type DummyMsg struct {
	A int
}

var dummyMsgType network.PacketTypeID

func newDummyProtocol(tni *TreeNodeInstance, conf DummyConfig, link chan bool) *DummyProtocol {
	return &DummyProtocol{tni, link, conf}
}

func (dm *DummyProtocol) Start() error {
	dm.link <- true
	if dm.config.Send {
		// also send to the children if any
		if !dm.IsLeaf() {
			if err := dm.SendToChildren(&DummyMsg{}); err != nil {
				log.Error(err)
			}
		}
	}
	return nil
}

func (dm *DummyProtocol) ProcessProtocolMsg(msg *ProtocolMsg) {
	dm.link <- true
}

// legacy reasons
func (dm *DummyProtocol) Dispatch() error {
	return nil
}

type DummyService struct {
	c        *Context
	path     string
	link     chan bool
	fakeTree *Tree
	firstTni *TreeNodeInstance
	Config   DummyConfig
}

func (ds *DummyService) ProcessClientRequest(path string, buf []byte) ([]byte, ClientError) {
	log.Lvl2("Got called with path", path, buf)
	msg := &DummyMsg{}
	err := protobuf.Decode(buf, msg)
	if err != nil {
		ds.link <- false
		return nil, NewClientErrorCode(4100, "wrong message")
	}
	if ds.firstTni == nil {
		ds.firstTni = ds.c.NewTreeNodeInstance(ds.fakeTree, ds.fakeTree.Root, dummyServiceName)
	}

	dp := newDummyProtocol(ds.firstTni, ds.Config, ds.link)

	if err := ds.c.RegisterProtocolInstance(dp); err != nil {
		ds.link <- false
		return nil, NewClientErrorCode(4101, "")
	}
	log.Lvl2("Starting protocol")
	go dp.Start()
	return nil, nil
}

func (ds *DummyService) NewProtocol(tn *TreeNodeInstance, conf *GenericConfig) (ProtocolInstance, error) {
	dp := newDummyProtocol(tn, DummyConfig{}, ds.link)
	return dp, nil
}

func (ds *DummyService) Process(packet *network.Packet) {
	if packet.MsgType != dummyMsgType {
		ds.link <- false
		return
	}
	dms := packet.Msg.(DummyMsg)
	if dms.A != 10 {
		ds.link <- false
		return
	}
	ds.link <- true
}

type ServiceMessages struct {
	*ServiceProcessor
	GotResponse chan bool
}

func (i *ServiceMessages) SimpleResponse(msg *network.Packet) {
	i.GotResponse <- true
}

func newServiceMessages(c *Context, path string) Service {
	s := &ServiceMessages{
		ServiceProcessor: NewServiceProcessor(c),
		GotResponse:      make(chan bool),
	}
	c.RegisterProcessorFunc(SimpleResponseType, s.SimpleResponse)
	return s
}

func waitOrFatalValue(ch chan bool, v bool, t *testing.T) {
	select {
	case b := <-ch:
		if v != b {
			t.Fatal("Wrong value returned on channel")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Waited too long")
	}

}

func waitOrFatal(ch chan bool, t *testing.T) {
	select {
	case _ = <-ch:
		return
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Waited too long")
	}
}
