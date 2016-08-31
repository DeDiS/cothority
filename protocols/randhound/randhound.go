package randhound

import (
	"bytes"
	"encoding/binary"
	"sync"
	"time"

	"github.com/dedis/cothority/crypto"
	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/sda"
	"github.com/dedis/crypto/abstract"
	"github.com/dedis/crypto/random"
)

// TODO:
// - Client commitments to the final list of secrets that will be used for the
//	 randomness; currently simply all secrets are used
// - Create transcript
// - Verify transcript
// - Hashing of I-messages and client-side verification
// - Handling of failing encryption/decryption proofs
// - Sane conditions on client-side when to proceed
// - import / export transcript in JSON

func init() {
	sda.ProtocolRegisterName("RandHound", NewRandHound)
}

// RandHound ...
type RandHound struct {
	*sda.TreeNodeInstance

	// Session information
	Nodes   int       // Total number of nodes (client + servers)
	Faulty  int       // Maximum number of Byzantine servers
	Purpose string    // Purpose of the protocol run
	Time    time.Time // Timestamp of initiation
	Rand    []byte    // Client-chosen randomness
	SID     []byte    // Session identifier
	Group   []*Group  // Server grouping

	// Auxiliary information
	ServerIdxToGroupNum []int // Mapping of gloabl server index to group number
	ServerIdxToGroupIdx []int // Mapping of global server index to group server index

	// For signaling the end of a protocol run
	Done chan bool

	// XXX: Dummy, remove later
	counter int
}

// Group ...
type Group struct {
	Server    []*sda.TreeNode          // Servers of the group
	Threshold int                      // Secret sharing threshold
	Idx       []int                    // Global indices of servers (= ServerIdentityIdx)
	Key       []abstract.Point         // Public keys of servers
	R1s       map[int]*R1              // R1 messages received from servers
	R2s       map[int]*R2              // R2 messages received from servers
	Commit    map[int][]abstract.Point // Commitments of server polynomials
	mutex     sync.Mutex
}

// I1 message
type I1 struct {
	SID       []byte           // Session identifier
	Threshold int              // Secret sharing threshold
	Key       []abstract.Point // Public keys of trustees
}

// R1 message
type R1 struct {
	HI1        []byte           // Hash of I1
	EncShare   []abstract.Point // Encrypted Shares
	EncProof   []ProofCore      // Encryption consistency proofs
	CommitPoly []byte           // Marshalled commitment polynomial
}

// I2 message
type I2 struct {
	SID      []byte           // Session identifier
	EncShare []abstract.Point // Encrypted shares
	EncProof []ProofCore      // Encryption consistency proofs
	Commit   []abstract.Point // Polynomial commitments
}

// R2 message
type R2 struct {
	HI2      []byte           // Hash of I2
	DecShare []abstract.Point // Decrypted shares
	DecProof []ProofCore      // Decryption consistency proofs
}

// WI1 is a SDA-wrapper around I1
type WI1 struct {
	*sda.TreeNode
	I1
}

// WR1 is a SDA-wrapper around R1
type WR1 struct {
	*sda.TreeNode
	R1
}

// WI2 is a SDA-wrapper around I2
type WI2 struct {
	*sda.TreeNode
	I2
}

// WR2 is a SDA-wrapper around R2
type WR2 struct {
	*sda.TreeNode
	R2
}

// NewRandHound ...
func NewRandHound(node *sda.TreeNodeInstance) (sda.ProtocolInstance, error) {

	// Setup RandHound protocol struct
	rh := &RandHound{
		TreeNodeInstance: node,
	}

	// Setup message handlers
	h := []interface{}{
		rh.handleI1, rh.handleI2,
		rh.handleR1, rh.handleR2,
	}
	err := rh.RegisterHandlers(h...)

	return rh, err
}

// Setup ...
func (rh *RandHound) Setup(nodes int, faulty int, groups int, purpose string) error {

	rh.Nodes = nodes
	rh.Faulty = faulty
	rh.Purpose = purpose
	rh.Group = make([]*Group, groups)
	rh.Done = make(chan bool, 1)
	rh.counter = 0

	return nil
}

// SessionID ...
func (rh *RandHound) SessionID() ([]byte, error) {

	buf := new(bytes.Buffer)

	if err := binary.Write(buf, binary.LittleEndian, uint32(rh.Nodes)); err != nil {
		return nil, err
	}

	if err := binary.Write(buf, binary.LittleEndian, uint32(rh.Faulty)); err != nil {
		return nil, err
	}

	//if err := binary.Write(buf, binary.LittleEndian, uint32(len(rh.Group))); err != nil {
	//	return nil, err
	//}

	if _, err := buf.WriteString(rh.Purpose); err != nil {
		return nil, err
	}

	t, err := rh.Time.MarshalBinary()
	if err != nil {
		return nil, err
	}

	if _, err := buf.Write(t); err != nil {
		return nil, err
	}

	if _, err := buf.Write(rh.Rand); err != nil {
		return nil, err
	}

	for _, group := range rh.Group {

		// Write threshold
		if err := binary.Write(buf, binary.LittleEndian, uint32(group.Threshold)); err != nil {
			return nil, err
		}

		// Write keys
		for _, k := range group.Key {
			kb, err := k.MarshalBinary()
			if err != nil {
				return nil, err
			}
			if _, err := buf.Write(kb); err != nil {
				return nil, err
			}
		}

		// XXX: Write indices?
	}

	return crypto.HashBytes(rh.Suite().Hash(), buf.Bytes())
}

// Start ...
func (rh *RandHound) Start() error {

	// Set timestamp
	rh.Time = time.Now()

	// Choose randomness
	hs := rh.Suite().Hash().Size()
	rand := make([]byte, hs)
	random.Stream.XORKeyStream(rand, rand)
	rh.Rand = rand

	// Determine server grouping
	serverGroup, keyGroup, err := rh.Shard(rand, len(rh.Group))
	if err != nil {
		return err
	}

	rh.ServerIdxToGroupNum = make([]int, rh.Nodes)
	rh.ServerIdxToGroupIdx = make([]int, rh.Nodes)
	for i, group := range serverGroup {

		g := &Group{
			Server:    group,
			Threshold: 2 * len(keyGroup[i]) / 3,
			Idx:       make([]int, len(group)),
			Key:       keyGroup[i],
			R1s:       make(map[int]*R1),
			R2s:       make(map[int]*R2),
			Commit:    make(map[int][]abstract.Point),
		}

		for j, server := range group {
			g.Idx[j] = server.ServerIdentityIdx                  // Record global server indices (=ServerIdentityIdx) that belong to this group
			rh.ServerIdxToGroupNum[server.ServerIdentityIdx] = i // Record group number the server belongs to
			rh.ServerIdxToGroupIdx[server.ServerIdentityIdx] = j // Record the group index of the server
		}

		rh.Group[i] = g
	}

	sid, err := rh.SessionID()
	if err != nil {
		return err
	}
	rh.SID = sid

	for i, group := range serverGroup {

		i1 := &I1{
			SID:       sid,
			Threshold: rh.Group[i].Threshold,
			Key:       rh.Group[i].Key,
		}

		if err := rh.Multicast(i1, group...); err != nil {
			return err
		}
	}
	return nil
}

func (rh *RandHound) handleI1(i1 WI1) error {

	msg := &i1.I1

	// Init PVSS and create shares
	H, _ := rh.Suite().Point().Pick(nil, rh.Suite().Cipher(msg.SID))
	pvss := NewPVSS(rh.Suite(), H, msg.Threshold)
	encShare, encProof, pb, err := pvss.Split(msg.Key, nil)
	if err != nil {
		return err
	}

	hi1 := []byte{1}
	r1 := &R1{HI1: hi1, EncShare: encShare, EncProof: encProof, CommitPoly: pb}
	return rh.SendTo(rh.Root(), r1)
}

func (rh *RandHound) handleR1(r1 WR1) error {

	msg := &r1.R1
	//log.Lvlf1("RandHound - R1: %v\n", rh.index())

	idx := r1.ServerIdentityIdx
	grp := rh.ServerIdxToGroupNum[idx]

	n := len(rh.Group[grp].Key)
	pbx := make([][]byte, n)
	index := make([]int, n)
	for i := 0; i < n; i++ {
		pbx[i] = msg.CommitPoly
		index[i] = i
	}

	// XXX: Store shares in a "sorted way" for later

	// Init PVSS and recover commits
	H, _ := rh.Suite().Point().Pick(nil, rh.Suite().Cipher(rh.SID))
	pvss := NewPVSS(rh.Suite(), H, rh.Group[grp].Threshold)
	commit, err := pvss.Commits(pbx, index)
	if err != nil {
		return err
	}

	// Verify encrypted shares
	f, err := pvss.Verify(H, rh.Group[grp].Key, commit, msg.EncShare, msg.EncProof)
	if err != nil {
		// Erase invalid data
		for i := range f {
			commit[i] = nil
			msg.EncShare[i] = nil
			msg.EncProof[i] = ProofCore{}
		}
	}

	// Record commit and message
	rh.Group[grp].mutex.Lock()
	rh.Group[grp].Commit[idx] = commit
	rh.Group[grp].R1s[idx] = msg
	rh.Group[grp].mutex.Unlock()

	// Continue once "enough" R1 messages have been collected
	if len(rh.Group[grp].R1s) == len(rh.Group[grp].Key) {

		n := len(rh.Group[grp].Idx)
		for i := 0; i < n; i++ {

			// Collect all shares, proofs, and commits intended for server i
			encShare := make([]abstract.Point, n)
			encProof := make([]ProofCore, n)
			commit := make([]abstract.Point, n)

			//  j is the group server index, k is the global server index
			for j, k := range rh.Group[grp].Idx {
				r1 := rh.Group[grp].R1s[k]
				encShare[j] = r1.EncShare[i]
				encProof[j] = r1.EncProof[i]
				commit[j] = rh.Group[grp].Commit[k][i]
			}

			i2 := &I2{
				SID:      rh.SID,
				EncShare: encShare,
				EncProof: encProof,
				Commit:   commit,
			}

			if err := rh.SendTo(rh.Group[grp].Server[i], i2); err != nil {
				return err
			}
		}
	}
	return nil
}

func (rh *RandHound) handleI2(i2 WI2) error {

	msg := &i2.I2
	//log.Lvlf1("RandHound - I2: %v\n", rh.index())

	// Map SID to base point H
	H, _ := rh.Suite().Point().Pick(nil, rh.Suite().Cipher(msg.SID))

	// Prepare data
	n := len(msg.EncShare)
	X := make([]abstract.Point, n)
	x := make([]abstract.Scalar, n)
	for i := 0; i < n; i++ {
		X[i] = rh.Public()
		x[i] = rh.Private()
	}

	// Verify encryption consistency proof
	pvss := NewPVSS(rh.Suite(), H, 0)
	f, err := pvss.Verify(H, X, msg.Commit, msg.EncShare, msg.EncProof)
	if err != nil {
		// Erase invalid data
		for i := range f {
			msg.Commit[i] = nil
			msg.EncShare[i] = nil
			msg.EncProof[i] = ProofCore{} // XXX: hack
		}
	}
	//log.Lvlf1("RandHound - I2 - Encryption verification passed: %v\n", rh.Index())

	// Decrypt shares
	decShare, decProof, err := pvss.Reveal(rh.Private(), msg.EncShare)
	if err != nil {
		return err
	}

	hi2 := []byte{3}
	r2 := &R2{HI2: hi2, DecShare: decShare, DecProof: decProof}
	return rh.SendTo(rh.Root(), r2)
}

func (rh *RandHound) handleR2(r2 WR2) error {

	msg := &r2.R2

	idx := r2.ServerIdentityIdx
	grp := rh.ServerIdxToGroupNum[idx]

	n := len(msg.DecShare)
	X := make([]abstract.Point, n)
	sX := make([]abstract.Point, n)

	group := rh.Group[grp]
	for i := 0; i < n; i++ {
		X[i] = r2.ServerIdentity.Public
	}

	// Get encrypted shares intended for server idx
	i := rh.ServerIdxToGroupIdx[idx]
	for j, k := range group.Idx {
		r1 := rh.Group[grp].R1s[k]
		sX[j] = r1.EncShare[i]
	}

	// Init PVSS and verify shares
	H, _ := rh.Suite().Point().Pick(nil, rh.Suite().Cipher(rh.SID))
	pvss := NewPVSS(rh.Suite(), H, rh.Group[grp].Threshold)
	f, err := pvss.Verify(rh.Suite().Point().Base(), msg.DecShare, X, sX, msg.DecProof)
	if err != nil {
		// Erase invalid data
		for i := range f {
			msg.DecShare[i] = nil
			msg.DecProof[i] = ProofCore{}
		}
	}

	rh.Group[grp].mutex.Lock()
	rh.Group[grp].R2s[idx] = msg
	rh.counter++
	rh.Group[grp].mutex.Unlock()

	// Continue once "enough" R2 messages have been collected
	// XXX: this check should be replaced by a more sane one
	if rh.counter == rh.Nodes-1 {

		rnd := rh.Suite().Point().Null()
		for i, group := range rh.Group {
			pvss := NewPVSS(rh.Suite(), H, group.Threshold)

			for _, j := range group.Idx {
				ps, err := pvss.Recover(rh.Group[i].R2s[j].DecShare)
				if err != nil {
					return err
				}
				rnd = rh.Suite().Point().Add(rnd, ps)
			}
		}

		log.Lvlf1("RandHound - collective randomness: %v", rnd)

		rh.Done <- true
	}
	return nil
}

func createTranscript() {}
func verifyTranscript() {}
