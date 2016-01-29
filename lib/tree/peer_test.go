package tree_test

import (
	"github.com/dedis/cothority/lib/tree"
	"github.com/dedis/crypto/edwards"
	"strconv"
	"testing"
)

func TestNewPeerListLocal(t *testing.T) {
	s := edwards.NewAES128SHA256Ed25519(false)
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
