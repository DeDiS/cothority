package tree_test

import (
	"strconv"
	"testing"

	"github.com/dedis/cothority/lib/network"
	"github.com/dedis/cothority/lib/testutil"
	"github.com/dedis/cothority/lib/tree"
)

func TestNewPeerListLocal(t *testing.T) {
	defer testutil.AfterTest(t)
	s := network.Suite
	nPeers := 11
	names := genLocalhostPeerNames(nPeers, 2000)
	pl := tree.GenPeerList(s, names)
	if len(pl.Peers) != 11 {
		t.Fatal("Did not get 11 peers")
	}
	for i, p := range pl.Peers {
		if p.Name != "localhost"+strconv.Itoa(2000+i) {
			t.Fatal("Peer", i, "is not from localhost")
		}
	}
}
