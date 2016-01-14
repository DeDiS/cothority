// topology is a general
package sda

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	"github.com/dedis/crypto/abstract"
	"github.com/dedis/crypto/edwards"
	"github.com/satori/go.uuid"
)

// In this file we define the main structures used for a running protocol
// instance. First there is the Entity struct: it represents the Entity of
// someone, a server over the internet, mainly tied by its public key.
// The tree contains the peerId which is the ID given to a an Entity / server
// during one protocol instance. A server can have many peerId in one tree.
// ProtocolInstance needs to know:
// - which EntityList we are using ( a selection of proper servers )
// - which Tree we are using.
// - The overlay network: a mapping from PeerId
// It contains the PeerId of the parent and the sub tree of the children.
func init() {
	network.RegisterProtocolType(TreeType, Tree{})
	network.RegisterProtocolType(TreeMarshalType, TreeMarshal{})
	network.RegisterProtocolType(TreeNodeType, TreeNode{})
	network.RegisterProtocolType(EntityListType, EntityList{})
}

// Tree is a topology to be used by any network layer/host layer
// It contains the peer list we use, and the tree we use
type Tree struct {
	Id     uuid.UUID
	IdList *EntityList
	Root   *TreeNode
}

// NewTree creates a new tree using the entityList and the root-node. It
// also generates the id.
func NewTree(il *EntityList, r *TreeNode) *Tree {
	r.UpdateIds()
	url := "https://dedis.epfl.ch/tree/" + il.Id.String() + r.Id.String()
	return &Tree{
		IdList: il,
		Root:   r,
		Id:     uuid.NewV5(uuid.NamespaceURL, url),
	}
}

// NewTreeFromMarshal takes a slice of bytes and an EntityList to re-create
// the original tree
func NewTreeFromMarshal(buf []byte, il *EntityList) (*Tree, error) {
	tp, pm, err := network.UnmarshalRegisteredType(buf,
		network.DefaultConstructors(edwards.NewAES128SHA256Ed25519(false)))
	if err != nil {
		return nil, err
	}
	if tp != TreeMarshalType {
		return nil, errors.New("Didn't receive TreeMarshal-struct")
	}
	dbg.Lvl4("TreeMarshal is", pm.(TreeMarshal))
	return pm.(TreeMarshal).MakeTree(il)
}

// MakeTreeMarshal creates a replacement-tree that is safe to send: no
// parent (creates loops), only sends ids (not send the entityList again)
func (t *Tree) MakeTreeMarshal() *TreeMarshal {
	if t.IdList == nil {
		return &TreeMarshal{}
	}
	treeM := &TreeMarshal{
		Node:   t.Id,
		Entity: t.IdList.Id,
	}
	treeM.Children = append(treeM.Children, TreeMarshalCopyTree(t.Root))
	dbg.Lvlf4("TreeMarshal is %+v", treeM)
	return treeM
}

// Marshal creates a simple binary-representation of the tree containing only
// the ids of the elements. Use NewTreeFromMarshal to get back the original
// tree
func (t *Tree) Marshal() ([]byte, error) {
	buf, err := network.MarshalRegisteredType(t.MakeTreeMarshal())
	return buf, err
}

// Equal verifies if the given tree is equal
func (t *Tree) Equal(t2 *Tree) bool {
	if t.Id != t2.Id || t.IdList.Id != t2.IdList.Id {
		dbg.Lvl4("Ids of trees don't match")
		return false
	}
	return t.Root.Equal(t2.Root)
}

// String writes the definition of the tree
func (t *Tree) String() string {
	return fmt.Sprintf("TreeId:%s - EntityListId:%s - RootId:%s",
		t.Id, t.IdList.Id, t.Root.Id)
}

// TreeMarshal is used to send and receive a tree-structure without having
// to copy the whole nodelist
type TreeMarshal struct {
	// This is the UUID of the corresponding TreeNode, or the Tree-Id for the
	// top-node
	Node uuid.UUID
	// This is the UUID of the Entity, except for the top-node, where this
	// is the EntityList-Id
	Entity uuid.UUID
	// All children from this tree. The top-node only has one child, which is
	// the root
	Children []*TreeMarshal
}

// TreeMarshalCopyTree takes a TreeNode and returns a corresponding
// TreeMarshal
func TreeMarshalCopyTree(tr *TreeNode) *TreeMarshal {
	tm := &TreeMarshal{
		Node:   tr.Id,
		Entity: tr.NodeId.Id,
	}
	for _, c := range tr.Children {
		tm.Children = append(tm.Children,
			TreeMarshalCopyTree(c))
	}
	return tm
}

// MakeTree creates a tree given an EntityList
func (tm TreeMarshal) MakeTree(il *EntityList) (*Tree, error) {
	if il.Id != tm.Entity {
		return nil, errors.New("Not correct EntityList-Id")
	}
	tree := &Tree{
		Id:     tm.Node,
		IdList: il,
	}
	tree.Root = tm.Children[0].MakeTreeFromList(il)
	return tree, nil
}

// MakeTreeFromList creates a sub-tree given an EntityList
func (tm *TreeMarshal) MakeTreeFromList(il *EntityList) *TreeNode {
	tn := &TreeNode{
		Id:     tm.Node,
		NodeId: il.Search(tm.Entity),
	}
	for _, c := range tm.Children {
		tn.Children = append(tn.Children, c.MakeTreeFromList(il))
	}
	return tn
}

// A PeerList is a list of Entity we choose to run  some tree on it ( and
// therefor some protocols)
type EntityList struct {
	Id uuid.UUID
	// TODO make that a set / map so search is O(1)
	List []*network.Entity
}

var NilEntityList = EntityList{}

// NewEntityList creates a new Entity from a list of entities. It also
// adds a UUID which is randomly chosen.
func NewEntityList(ids []*network.Entity) *EntityList {
	url := "https://dedis.epfl.ch/entityList/"
	for _, i := range ids {
		url += i.Id.String()
	}
	return &EntityList{
		List: ids,
		Id:   uuid.NewV5(uuid.NamespaceURL, url),
	}
}

// Search looks for a corresponding UUID and returns that entity
func (il *EntityList) Search(uuid uuid.UUID) *network.Entity {
	for _, i := range il.List {
		if i.Id == uuid {
			return i
		}
	}
	return nil
}

// TreeNode is one node in the tree
type TreeNode struct {
	// The Id represents that node of the tree
	Id uuid.UUID
	// The NodeID points to the corresponding host. One given host
	// can be used more than once in a tree.
	NodeId   *network.Entity
	Parent   *TreeNode
	Children []*TreeNode
}

// Check if it can communicate with parent or children
func (t *TreeNode) IsConnectedTo(id *network.Entity) bool {
	if t.Parent != nil && t.Parent.NodeId == id {
		return true
	}

	for i := range t.Children {
		if t.Children[i].NodeId == id {
			return true
		}
	}
	return false
}

// IsLeaf returns true for a node without children
func (t *TreeNode) IsLeaf() bool {
	return len(t.Children) == 0
}

// IsRoot returns true for a node without a parent
func (t *TreeNode) IsRoot() bool {
	return t.Parent == nil
}

// AddChild adds a child to this tree-node. Once the tree is set up, the
// function 'UpdateIds' should be called
func (t *TreeNode) AddChild(c *TreeNode) {
	t.Children = append(t.Children, c)
	c.Parent = t
}

// UpdateIds should be called on the root-node, so that it recursively
// calculates the whole tree as a merkle-tree
func (t *TreeNode) UpdateIds() {
	url := "https://dedis.epfl.ch/treenode/" + t.NodeId.Id.String()
	for _, child := range t.Children {
		child.UpdateIds()
		url += child.Id.String()
	}
	t.Id = uuid.NewV5(uuid.NamespaceURL, url)
}

// Equal tests if that node is equal to the given node
func (t *TreeNode) Equal(t2 *TreeNode) bool {
	if t.Id != t2.Id || t.NodeId.Id != t2.NodeId.Id {
		dbg.Lvl4("TreeNode: ids are not equal")
		return false
	}
	if len(t.Children) != len(t2.Children) {
		dbg.Lvl4("TreeNode: number of children are not equal")
		return false
	}
	for i, c := range t.Children {
		if !c.Equal(t2.Children[i]) {
			dbg.Lvl4("TreeNode: children are not equal")
			return false
		}
	}
	return true
}

// NewTreeNode creates a new TreeNode with the proper Id
func NewTreeNode(ni *network.Entity) *TreeNode {
	tn := &TreeNode{
		NodeId:   ni,
		Parent:   nil,
		Children: make([]*TreeNode, 0),
	}
	tn.UpdateIds()
	return tn
}

// String returns the current treenode's Id as a string.
func (t *TreeNode) String() string {
	return string(t.Id.String())
}

// Stringify returns a string containing the whole tree.
func (t *TreeNode) Stringify() string {
	var buf bytes.Buffer
	var lastDepth int
	fn := func(d int, n *TreeNode) {
		if d > lastDepth {
			buf.Write([]byte("\n\n"))
		} else {
			buf.Write([]byte(n.Id.String()))
		}
	}
	t.Visit(0, fn)
	return buf.String()
}

// Visit is a recursive function that allows for depth-first calling on all
// nodes
func (t *TreeNode) Visit(firstDepth int, fn func(depth int, n *TreeNode)) {
	fn(firstDepth, t)
	for i := range t.Children {
		t.Children[i].Visit(firstDepth+1, fn)
	}
}

// EntityListToml is the struct can can embbed EntityToml to be written in a
// toml file
type EntityListToml struct {
	Id   uuid.UUID
	List []*network.EntityToml
}

// Toml returns the toml-writtable version of this entityList
func (id *EntityList) Toml(suite abstract.Suite) *EntityListToml {
	ids := make([]*network.EntityToml, len(id.List))
	for i := range id.List {
		ids[i] = id.List[i].Toml(suite)
	}
	return &EntityListToml{
		Id:   id.Id,
		List: ids,
	}
}

// EntityList returns the Id list from this toml read struct
func (id *EntityListToml) EntityList(suite abstract.Suite) *EntityList {
	ids := make([]*network.Entity, len(id.List))
	for i := range id.List {
		ids[i] = id.List[i].Entity(suite)
	}
	return &EntityList{
		Id:   id.Id,
		List: ids,
	}
}

const (
	TopologyType = iota + 200
	TreeNodeType
	TreeMarshalType
	TreeType
	EntityType
	EntityListType
)

/*
Id is not used for the moment, rather a static, random UUID is used.
func (t *TreeNode) Id() UUID {
	buf := NewHashFunc()
	if t.Parent != "" {
		buf.Write([]byte(t.Parent))
	}
	buf.Write([]byte(t.PeerId))
	for i := range t.Children {
		buf.Write([]byte(t.Children[i].PeerId))
	}
	return UUID(buf.Sum(nil))
}

func (t *Tree) Id() UUID {
	h := NewHashFunc()
	h.Write([]byte(t.IdList.Id))
	h.Write([]byte(t.Root.Id()))
	return UUID(h.Sum(nil))
}

// generateId is not used for the moment, as we decided to use UUIDs, which
// are random. But perhaps it would be a good idea to switch back to
// something depending on public-key hashes anyway.
func generateId(ids []*Entity) UUID {
	h := NewHashFunc()
	for _, i := range ids {
		b, _ := i.Public.MarshalBinary()
		h.Write(b)
	}
	return UUID(h.Sum(nil))
}


*/
