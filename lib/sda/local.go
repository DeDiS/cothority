package sda

import (
	"errors"
	"fmt"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	"github.com/dedis/crypto/abstract"
	"github.com/dedis/crypto/config"
	"github.com/satori/go.uuid"
	"strconv"
	"testing"
	"time"
)

type LocalTest struct {
	// A map of Entity.Id to Hosts
	Hosts map[uuid.UUID]*Host
	// A map of Entity.Id to Overlays
	Overlays map[uuid.UUID]*Overlay
	// A map of EntityList.Id to EntityLists
	EntityLists map[uuid.UUID]*EntityList
	// A map of Tree.Id to Trees
	Trees map[uuid.UUID]*Tree
}

// NewLocalTest creates a new Local handler that can be used to test protocols
// locally
func NewLocalTest() *LocalTest {
	dbg.TestOutput(testing.Verbose(), 4)
	return &LocalTest{
		Hosts:       make(map[uuid.UUID]*Host),
		Overlays:    make(map[uuid.UUID]*Overlay),
		EntityLists: make(map[uuid.UUID]*EntityList),
		Trees:       make(map[uuid.UUID]*Tree),
	}
}

// StartNewNodeName takes a name and a tree and will create a
// new Node with the protocol 'name' running from the tree-root
func (l *LocalTest) StartNewNodeName(name string, t *Tree) (*Node, error) {
	rootEntityId := t.Root.Entity.Id
	for _, h := range l.Hosts {
		if uuid.Equal(h.Entity.Id, rootEntityId) {
			// XXX do we really need multiples overlays ? Can't we just use the
			// Node, since it is already dispatched as like a TreeNode ?
			return l.Overlays[h.Entity.Id].StartNewNodeName(name, t)
		}
	}
	return nil, errors.New("Didn't find host for tree-root")
}

// GenTree will create a tree of n hosts. If connect is true, they will
// be connected to the root host. If register is true, the EntityList and Tree
// will be registered with the overlay.
func (l *LocalTest) GenTree(n int, connect bool, register bool) ([]*Host, *EntityList, *Tree) {
	hosts := GenLocalHosts(n, connect, true)
	for _, host := range hosts {
		l.Hosts[host.Entity.Id] = host
		l.Overlays[host.Entity.Id] = host.overlay
	}

	list := l.GenEntityListFromHost(hosts...)
	tree := list.GenerateBinaryTree()
	l.Trees[tree.Id] = tree
	if register {
		hosts[0].overlay.RegisterEntityList(list)
		hosts[0].overlay.RegisterTree(tree)
	}
	return hosts, list, tree
}

func (l *LocalTest) GenEntityListFromHost(hosts ...*Host) *EntityList {
	var entities []*network.Entity
	for i := range hosts {
		entities = append(entities, hosts[i].Entity)
	}
	list := NewEntityList(entities)
	l.EntityLists[list.Id] = list
	return list
}

// CloseAll takes a list of hosts that will be closed
func (l *LocalTest) CloseAll() {
	time.Sleep(time.Millisecond * 200)
	for _, host := range l.Hosts {
		err := host.Close()
		if err != nil {
			dbg.Error("Closing host", host, "gives error", err)
		}
	}
}

func (l *LocalTest) GetTree(tn *TreeNode) *Tree {
	var tree *Tree
	for _, t := range l.Trees {
		if tn.IsInTree(t) {
			tree = t
			break
		}
	}
	return tree
}

// NewNode creates a new node on a TreeNode
func (l *LocalTest) NewNode(tn *TreeNode, protName string) (*Node, error) {
	o := l.Overlays[tn.Entity.Id]
	if o == nil {
		return nil, errors.New("Didn't find corresponding overlay")
	}
	tree := l.GetTree(tn)
	if tree == nil {
		return nil, errors.New("Didn't find tree corresponding to TreeNode")
	}
	protId := ProtocolNameToUuid(protName)
	if !ProtocolExists(protId) {
		return nil, errors.New("Didn't find protocol: " + protName)
	}
	tok := &Token{
		ProtocolID:   protId,
		EntityListID: tree.EntityList.Id,
		TreeID:       tree.Id,
		TreeNodeID:   tn.Id,
		RoundID:      uuid.NewV4(),
	}
	return NewNode(o, tok)
}

// GetNodes returns all Nodes that belong to a treeNode
func (l *LocalTest) GetNodes(tn *TreeNode) []*Node {
	nodes := make([]*Node, 0)
	for _, n := range l.Overlays[tn.Entity.Id].nodes {
		nodes = append(nodes, n)
	}
	return nodes
}

func (l *LocalTest) SendTreeNode(proto string, from, to *Node, msg network.ProtocolMessage) error {
	if from.Tree().Id != to.Tree().Id {
		return errors.New("Can't send from one tree to another")
	}
	sdaMsg := &SDAData{
		Msg:     msg,
		MsgType: network.TypeToUUID(msg),
		From:    from.token,
		To:      to.token,
	}
	return to.overlay.TransmitMsg(sdaMsg)
}

func (l *LocalTest) AddPendingTreeMarshal(h *Host, tm *TreeMarshal) {
	h.addPendingTreeMarshal(tm)
}

func (l *LocalTest) CheckPendingTreeMarshal(h *Host, el *EntityList) {
	h.checkPendingTreeMarshal(el)
}

func (l *LocalTest) NodesFromOverlay(entityId uuid.UUID) map[uuid.UUID]*Node {
	return l.Overlays[entityId].nodes
}

func (l *LocalTest) AllNodes() []*Node {
	var nodes []*Node
	for h := range l.Hosts {
		overlay := l.Hosts[h].overlay
		for i := range overlay.nodes {
			fmt.Println("Addind nodes")
			nodes = append(nodes, overlay.nodes[i])
		}
	}
	return nodes
}

// NewLocalHost creates a new host with the given address and registers it
func NewLocalHost(port int) *Host {
	address := "localhost:" + strconv.Itoa(port)
	priv, pub := PrivPub()
	id := network.NewEntity(pub, address)
	return NewHost(id, priv)
}

// GenLocalHosts will create n hosts with the first one being connected to each of
// the other nodes if connect is true
func GenLocalHosts(n int, connect bool, processMessages bool) []*Host {
	var hosts []*Host
	for i := 0; i < n; i++ {
		host := NewLocalHost(2000 + i*10)
		hosts = append(hosts, host)
	}
	root := hosts[0]
	for _, host := range hosts {
		host.Listen()
		if processMessages {
			go host.ProcessMessages()
		}
		if connect {
			if _, err := host.Connect(root.Entity); err != nil {
				dbg.Fatal("Could not connect hosts")
			}
		}
	}
	return hosts
}

// PrivPub creates a private/public key pair
func PrivPub() (abstract.Secret, abstract.Point) {
	keypair := config.NewKeyPair(network.Suite)
	return keypair.Secret, keypair.Public
}
