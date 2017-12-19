package skipchain_test

import (
	"testing"

	"github.com/dedis/cothority/skipchain"
	"github.com/dedis/kyber/sign/schnorr"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"github.com/stretchr/testify/require"
)

const tsName = "tsName"

var tsID onet.ServiceID
var tSuite = skipchain.Suite

func init() {
	var err error
	tsID, err = onet.RegisterNewService(tsName, newTestService)
	log.ErrFatal(err)
}

// TestGU tests the GetUpdate message
func TestGU(t *testing.T) {
	local := onet.NewLocalTest(tSuite)
	defer local.CloseAll()
	servers, ro, _ := local.GenTree(2, true)
	tss := local.GetServices(servers, tsID)

	ts0 := tss[0].(*testService)
	ts1 := tss[1].(*testService)
	sb0 := skipchain.NewSkipBlock()
	sb0.Roster = ro
	sb0.Hash = sb0.CalculateHash()
	sb1 := skipchain.NewSkipBlock()
	sb1.BackLinkIDs = []skipchain.SkipBlockID{sb0.Hash}
	sb1.Hash = sb1.CalculateHash()
	bl := &skipchain.BlockLink{Hash: sb1.Hash, Signature: []byte{}}
	sb0.ForwardLink = []*skipchain.BlockLink{bl}
	db, bucket := ts0.GetAdditionalBucket("skipblocks")
	ts0.Db = skipchain.NewSkipBlockDB(db, bucket)
	ts0.Db.Store(sb0)
	ts0.Db.Store(sb1)
	db, bucket = ts1.GetAdditionalBucket("skipblocks")
	ts1.Db = skipchain.NewSkipBlockDB(db, bucket)
	ts1.Db.Store(sb0)
	ts1.Db.Store(sb1)
	sb := ts1.CallGU(sb0)
	require.NotNil(t, sb)
	require.Equal(t, sb1.Hash, sb.Hash)
}

// TestER tests the ProtoExtendRoster message
func TestER(t *testing.T) {
	nodes := []int{2, 5, 13}
	for _, nbrNodes := range nodes {
		testER(t, tsID, nbrNodes)
	}
}

func testER(t *testing.T, tsid onet.ServiceID, nbrNodes int) {
	log.Lvl1("Testing", nbrNodes, "nodes")
	local := onet.NewLocalTest(tSuite)
	defer local.CloseAll()
	servers, roster, tree := local.GenBigTree(nbrNodes, nbrNodes, nbrNodes, true)
	tss := local.GetServices(servers, tsid)
	log.Lvl3(tree.Dump())

	sb := &skipchain.SkipBlock{SkipBlockFix: &skipchain.SkipBlockFix{Roster: roster,
		Data: []byte{}}}

	// Check refusing of new chains
	for _, t := range tss {
		t.(*testService).FollowerIDs = []skipchain.SkipBlockID{[]byte{0}}
	}
	ts := tss[0].(*testService)
	sigs := ts.CallER(tree, sb)
	require.Equal(t, 0, len(sigs))

	// Check inclusion of new chains
	for _, t := range tss {
		t.(*testService).Followers = []skipchain.FollowChainType{{
			Block:    sb,
			NewChain: skipchain.NewChainAnyNode,
		}}
	}
	sigs = ts.CallER(tree, sb)
	require.Equal(t, nbrNodes-1, len(sigs))

	for _, s := range sigs {
		_, si := roster.Search(s.SI)
		require.NotNil(t, si)
		require.Nil(t, schnorr.Verify(tSuite, si.Public, sb.SkipChainID(), s.Signature))
	}

	// Have only one node refuse
	if nbrNodes > 2 {
		for i := 2; i < nbrNodes; i++ {
			log.Lvl2("Checking failing signature at", i)
			tss[i].(*testService).FollowerIDs = []skipchain.SkipBlockID{[]byte{0}}
			tss[i].(*testService).Followers = []skipchain.FollowChainType{}
			sigs = ts.CallER(tree, sb)
			require.Equal(t, 0, len(sigs))
			tss[i].(*testService).Followers = []skipchain.FollowChainType{{
				Block:    sb,
				NewChain: skipchain.NewChainAnyNode,
			}}
		}
	}
}

type testService struct {
	*onet.ServiceProcessor
	Followers   []skipchain.FollowChainType
	FollowerIDs []skipchain.SkipBlockID
	Db          *skipchain.SkipBlockDB
}

func (ts *testService) CallER(t *onet.Tree, b *skipchain.SkipBlock) []skipchain.ProtoExtendSignature {
	pi, err := ts.CreateProtocol(skipchain.ProtocolExtendRoster, t)
	if err != nil {
		return []skipchain.ProtoExtendSignature{}
	}
	pisc := pi.(*skipchain.ExtendRoster)
	pisc.ExtendRoster = &skipchain.ProtoExtendRoster{Block: *b}
	if err := pi.Start(); err != nil {
		log.ErrFatal(err)
	}
	return <-pisc.ExtendRosterReply
}

func (ts *testService) CallGU(sb *skipchain.SkipBlock) *skipchain.SkipBlock {
	t := onet.NewRoster([]*network.ServerIdentity{ts.ServerIdentity(), sb.Roster.List[0]}).GenerateBinaryTree()
	pi, err := ts.CreateProtocol(skipchain.ProtocolGetUpdate, t)
	if err != nil {
		log.Error(err)
		return &skipchain.SkipBlock{}
	}
	pisc := pi.(*skipchain.GetUpdate)
	pisc.GetUpdate = &skipchain.ProtoGetUpdate{SBID: sb.Hash}
	if err := pi.Start(); err != nil {
		log.ErrFatal(err)
	}
	return <-pisc.GetUpdateReply
}

func (ts *testService) NewProtocol(ti *onet.TreeNodeInstance, conf *onet.GenericConfig) (pi onet.ProtocolInstance, err error) {
	if ti.ProtocolName() == skipchain.ProtocolExtendRoster {
		// Start by getting latest blocks of all followers
		pi, err = skipchain.NewProtocolExtendRoster(ti)
		if err == nil {
			pier := pi.(*skipchain.ExtendRoster)
			pier.Followers = &ts.Followers
			pier.FollowerIDs = ts.FollowerIDs
			pier.DB = ts.Db
		}
	}
	if ti.ProtocolName() == skipchain.ProtocolGetUpdate {
		pi, err = skipchain.NewProtocolGetUpdate(ti)
		if err == nil {
			pigu := pi.(*skipchain.GetUpdate)
			pigu.DB = ts.Db
		}
	}
	return
}

func newTestService(c *onet.Context) (onet.Service, error) {
	s := &testService{
		ServiceProcessor: onet.NewServiceProcessor(c),
	}
	return s, nil
}
