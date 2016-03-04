// topology is a general
package sda

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	"github.com/dedis/crypto/abstract"
	"github.com/satori/go.uuid"
	"net"
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

// Tree is a topology to be used by any network layer/host layer
// It contains the peer list we use, and the tree we use
type Tree struct {
	Id         uuid.UUID
	EntityList *EntityList
	Root       *TreeNode
}

var TreeType = network.RegisterMessageType(Tree{})

// NewTree creates a new tree using the entityList and the root-node. It
// also generates the id.
func NewTree(il *EntityList, r *TreeNode) *Tree {
	url := network.UuidURL + "tree/" + il.Id.String() + r.Id.String()
	return &Tree{
		EntityList: il,
		Root:       r,
		Id:         uuid.NewV5(uuid.NamespaceURL, url),
	}
}

// NewTreeFromMarshal takes a slice of bytes and an EntityList to re-create
// the original tree
func NewTreeFromMarshal(buf []byte, il *EntityList) (*Tree, error) {
	tp, pm, err := network.UnmarshalRegisteredType(buf,
		network.DefaultConstructors(network.Suite))
	if err != nil {
		return nil, err
	}
	if tp != TreeMarshalType {
		return nil, errors.New("Didn't receive TreeMarshal-struct")
	}
	return pm.(TreeMarshal).MakeTree(il)
}

// MakeTreeMarshal creates a replacement-tree that is safe to send: no
// parent (creates loops), only sends ids (not send the entityList again)
func (t *Tree) MakeTreeMarshal() *TreeMarshal {
	if t.EntityList == nil {
		return &TreeMarshal{}
	}
	treeM := &TreeMarshal{
		NodeId:   t.Id,
		EntityId: t.EntityList.Id,
	}
	treeM.Children = append(treeM.Children, TreeMarshalCopyTree(t.Root))
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
	if t.Id != t2.Id || t.EntityList.Id != t2.EntityList.Id {
		dbg.Lvl4("Ids of trees don't match")
		return false
	}
	return t.Root.Equal(t2.Root)
}

// String writes the definition of the tree
func (t *Tree) String() string {
	return fmt.Sprintf("TreeId:%s - EntityListId:%s - RootId:%s",
		t.Id, t.EntityList.Id, t.Root.Id)
}

// Dump returns string about the tree
func (t *Tree) Dump() string {
	ret := "Tree " + t.Id.String() + " is:"
	t.Root.Visit(0, func(d int, tn *TreeNode) {
		if tn.Parent != nil {
			ret += fmt.Sprintf("\n%2d - %s/%s has parent %s/%s", d,
				tn.Id, tn.Entity.Addresses,
				tn.Parent.Id, tn.Parent.Entity.Addresses)
		} else {
			ret += fmt.Sprintf("\n%s/%s is root", tn.Id, tn.Entity.Addresses)
		}
	})
	return ret
}

// GetTreeNode searches the tree for the given TreeNodeId
func (t *Tree) GetTreeNode(tn uuid.UUID) (ret *TreeNode) {
	found := func(d int, tns *TreeNode) {
		if tns.Id == tn {
			ret = tns
		}
	}
	t.Root.Visit(0, found)
	return ret
}

// ListNodes is used for testing
func (t *Tree) ListNodes() (ret []*TreeNode) {
	ret = make([]*TreeNode, 0)
	add := func(d int, tns *TreeNode) {
		ret = append(ret, tns)
	}
	t.Root.Visit(0, add)
	return ret
}

// IsBinary returns true if every node has two or no children
func (t *Tree) IsBinary(root *TreeNode) bool {
	return t.IsNary(root, 2)
}

// IsNary returns true if every node has two or no children
func (t *Tree) IsNary(root *TreeNode, N int) bool {
	nChild := len(root.Children)
	if nChild != N && nChild != 0 {
		dbg.Lvl3("Only", nChild, "children for", root.Id)
		return false
	}
	for _, c := range root.Children {
		if !t.IsNary(c, N) {
			return false
		}
	}
	return true
}

// Size returns the number of all TreeNodes
func (t *Tree) Size() int {
	size := 0
	t.Root.Visit(0, func(d int, tn *TreeNode) {
		size += 1
	})
	return size
}

// UsesList returns true if all Entities of the list are used at least once
// in the tree
func (t *Tree) UsesList() bool {
	nodes := t.ListNodes()
	for _, p := range t.EntityList.List {
		found := false
		for _, n := range nodes {
			if n.Entity.Id == p.Id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TreeMarshal is used to send and receive a tree-structure without having
// to copy the whole nodelist
type TreeMarshal struct {
	// This is the UUID of the corresponding TreeNode, or the Tree-Id for the
	// top-node
	NodeId uuid.UUID
	// This is the UUID of the Entity, except for the top-node, where this
	// is the EntityList-Id
	EntityId uuid.UUID
	// All children from this tree. The top-node only has one child, which is
	// the root
	Children []*TreeMarshal
}

func (tm *TreeMarshal) String() string {
	s := fmt.Sprintf("%v", tm.EntityId)
	s += "\n"
	for i := range tm.Children {
		s += tm.Children[i].String()
	}
	return s
}

var TreeMarshalType = network.RegisterMessageType(TreeMarshal{})

// TreeMarshalCopyTree takes a TreeNode and returns a corresponding
// TreeMarshal
func TreeMarshalCopyTree(tr *TreeNode) *TreeMarshal {
	tm := &TreeMarshal{
		NodeId:   tr.Id,
		EntityId: tr.Entity.Id,
	}
	for i := range tr.Children {
		tm.Children = append(tm.Children,
			TreeMarshalCopyTree(tr.Children[i]))
	}
	return tm
}

// MakeTree creates a tree given an EntityList
func (tm TreeMarshal) MakeTree(il *EntityList) (*Tree, error) {
	if il.Id != tm.EntityId {
		return nil, errors.New("Not correct EntityList-Id")
	}
	tree := &Tree{
		Id:         tm.NodeId,
		EntityList: il,
	}
	tree.Root = tm.Children[0].MakeTreeFromList(nil, il)
	return tree, nil
}

// MakeTreeFromList creates a sub-tree given an EntityList
func (tm *TreeMarshal) MakeTreeFromList(parent *TreeNode, il *EntityList) *TreeNode {
	tn := &TreeNode{
		Parent: parent,
		Id:     tm.NodeId,
		Entity: il.Search(tm.EntityId),
	}
	for _, c := range tm.Children {
		tn.Children = append(tn.Children, c.MakeTreeFromList(tn, il))
	}
	return tn
}

// An EntityList is a list of Entity we choose to run  some tree on it ( and
// therefor some protocols)
type EntityList struct {
	Id uuid.UUID
	// TODO make that a map so search is O(1)
	List []*network.Entity
	// Aggregate public key
	Aggregate abstract.Point
}

var EntityListType = network.RegisterMessageType(EntityList{})

var NilEntityList = EntityList{}

// NewEntityList creates a new Entity from a list of entities. It also
// adds a UUID which is randomly chosen.
func NewEntityList(ids []*network.Entity) *EntityList {
	// compute the aggregate key already
	agg := network.Suite.Point().Null()
	for _, e := range ids {
		agg = agg.Add(agg, e.Public)
	}
	return &EntityList{
		List:      ids,
		Aggregate: agg,
		Id:        uuid.NewV4(),
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

// Get simply returns the entity that is stored at that index in the entitylist
// returns nil if index error
func (en *EntityList) Get(idx int) *network.Entity {
	if idx < 0 || idx > len(en.List) {
		return nil
	}
	return en.List[idx]
}

// GenerateBigNaryTree creates a tree where each node has N children.
// It will make a tree with exactly 'nodes' elements, regardless of the
// size of the EntityList. If 'nodes' is bigger than the number of elements
// in the EntityList, it will add some or all elements in the EntityList
// more than once.
// If the length of the EntityList is equal to 'nodes', it is guaranteed that
// all Entities from the EntityList will be used in the tree.
// However, for some configurations it is impossible to use all Entities from
// the EntityList and still avoid having a parent and a child from the same
// host. In this case use-all has preference over not-the-same-host.
func (il *EntityList) GenerateBigNaryTree(N, nodes int) *Tree {
	// list of which hosts are already used
	used := make([]bool, len(il.List))
	ilLen := len(il.List)
	// only use all Entities if we have the same number of nodes and hosts
	useAll := ilLen == nodes
	root := NewTreeNode(il.List[0])
	used[0] = true
	levelNodes := []*TreeNode{root}
	totalNodes := 1
	elIndex := 1 % ilLen
	for totalNodes < nodes {
		newLevelNodes := make([]*TreeNode, len(levelNodes)*N)
		newLevelNodesCounter := 0
		for i, parent := range levelNodes {
			children := (nodes - totalNodes) * (i + 1) / len(levelNodes)
			if children > N {
				children = N
			}
			parent.Children = make([]*TreeNode, children)
			parentHost, _, _ := net.SplitHostPort(parent.Entity.Addresses[0])
			for n := 0; n < children; n++ {
				// Check on host-address, so that no child is
				// on the same host as the parent.
				childHost, _, _ := net.SplitHostPort(il.List[elIndex].Addresses[0])
				elIndexFirst := elIndex
				notSameHost := true
				for (notSameHost && childHost == parentHost && ilLen > 1) ||
					(useAll && used[elIndex]) {
					elIndex = (elIndex + 1) % ilLen
					if useAll && used[elIndex] {
						// In case we searched all Entities,
						// give up on finding another host, but
						// keep using all Entities
						if elIndex == elIndexFirst {
							notSameHost = false
						}
						continue
					}
					// If we tried all hosts, it means we're using
					// just one hostname, as we didn't find any
					// other name
					if elIndex == elIndexFirst {
						break
					}
					childHost, _, _ = net.SplitHostPort(il.List[elIndex].Addresses[0])
				}
				child := NewTreeNode(il.List[elIndex])
				used[elIndex] = true
				elIndex = (elIndex + 1) % ilLen
				totalNodes += 1
				parent.Children[n] = child
				child.Parent = parent
				newLevelNodes[newLevelNodesCounter] = child
				newLevelNodesCounter += 1
			}
		}
		levelNodes = newLevelNodes[:newLevelNodesCounter]
	}
	return NewTree(il, root)
}

// GenerateNaryTree creates a tree where each node has N children.
// The first element of the EntityList will be the root element.
func (il *EntityList) GenerateNaryTree(N int) *Tree {
	root := il.addNary(nil, N, 0, len(il.List)-1)
	return NewTree(il, root)
}

// addNary is a recursive function to create the binary tree
func (il *EntityList) addNary(parent *TreeNode, N, start, end int) *TreeNode {
	if start <= end && end < len(il.List) {
		node := NewTreeNode(il.List[start])
		if parent != nil {
			node.Parent = parent
			parent.Children = append(parent.Children, node)
		}
		diff := end - start
		for n := 0; n < N; n++ {
			s := diff * n / N
			e := diff * (n + 1) / N
			il.addNary(node, N, start+s+1, start+e)
		}
		return node
	} else {
		return nil
	}
}

// GenerateBinaryTree creates a binary tree out of the EntityList
// out of it. The first element of the EntityList will be the root element.
func (il *EntityList) GenerateBinaryTree() *Tree {
	return il.GenerateNaryTree(2)
}

// TreeNode is one node in the tree
type TreeNode struct {
	// The Id represents that node of the tree
	Id uuid.UUID
	// The Entity points to the corresponding host. One given host
	// can be used more than once in a tree.
	Entity   *network.Entity
	Parent   *TreeNode
	Children []*TreeNode
}

func (t *TreeNode) Name() string {
	return t.Entity.First()
}

var TreeNodeType = network.RegisterMessageType(TreeNode{})

// NewTreeNode creates a new TreeNode with the proper Id
func NewTreeNode(ni *network.Entity) *TreeNode {
	tn := &TreeNode{
		Entity:   ni,
		Parent:   nil,
		Children: make([]*TreeNode, 0),
		Id:       uuid.NewV4(),
	}
	return tn
}

// Check if it can communicate with parent or children
func (t *TreeNode) IsConnectedTo(e *network.Entity) bool {
	if t.Parent != nil && t.Parent.Entity.Equal(e) {
		return true
	}

	for i := range t.Children {
		if t.Children[i].Entity.Equal(e) {
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

// IsInTree - verifies if the TreeNode is in the given Tree
func (t *TreeNode) IsInTree(tree *Tree) bool {
	root := *t
	for root.Parent != nil {
		root = *root.Parent
	}
	return tree.Root.Id == root.Id
}

// AddChild adds a child to this tree-node.
func (t *TreeNode) AddChild(c *TreeNode) {
	t.Children = append(t.Children, c)
	c.Parent = t
}

// Equal tests if that node is equal to the given node
func (t *TreeNode) Equal(t2 *TreeNode) bool {
	if t.Id != t2.Id || t.Entity.Id != t2.Entity.Id {
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
	for _, c := range t.Children {
		c.Visit(firstDepth+1, fn)
	}
}

// EntityListToml is the struct can can embedded EntityToml to be written in a
// toml file
type EntityListToml struct {
	Id   uuid.UUID
	List []*network.EntityToml
}

// Toml returns the toml-writable version of this entityList
func (el *EntityList) Toml(suite abstract.Suite) *EntityListToml {
	ids := make([]*network.EntityToml, len(el.List))
	for i := range el.List {
		ids[i] = el.List[i].Toml(suite)
	}
	return &EntityListToml{
		Id:   el.Id,
		List: ids,
	}
}

// EntityList returns the Id list from this toml read struct
func (elt *EntityListToml) EntityList(suite abstract.Suite) *EntityList {
	ids := make([]*network.Entity, len(elt.List))
	for i := range elt.List {
		ids[i] = elt.List[i].Entity(suite)
	}
	return &EntityList{
		Id:   elt.Id,
		List: ids,
	}
}
