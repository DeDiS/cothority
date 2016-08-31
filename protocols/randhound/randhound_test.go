package randhound_test

import (
	"testing"
	"time"

	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/protocols/randhound"
	"github.com/dedis/cothority/sda"
)

func TestRandHound(t *testing.T) {

	// XXX: nodes 17, groups 3 fails

	var name = "RandHound"
	var nodes int = 21
	var faulty int = 2
	var groups int = 4
	var purpose string = "RandHound test run"

	_ = faulty
	_ = purpose

	local := sda.NewLocalTest()
	_, _, tree := local.GenTree(int(nodes), false, true, true)
	defer local.CloseAll()

	// Setup and start RandHound

	log.Lvlf1("RandHound - starting")
	protocol, err := local.CreateProtocol(name, tree)
	if err != nil {
		t.Fatal("Couldn't initialise RandHound protocol:", err)
	}
	rh := protocol.(*randhound.RandHound)
	err = rh.Setup(nodes, faulty, groups, purpose)
	if err != nil {
		t.Fatal("Couldn't initialise RandHound protocol:", err)
	}
	//log.Lvlf1("RandHound - group config: %d %d %d %d %d %d\n", rh.Group.N, rh.Group.F, rh.Group.L, rh.Group.K, rh.Group.R, rh.Group.T)
	//log.Lvlf1("RandHound - groups: %d\n", groups)
	if err := protocol.Start(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-rh.Done:
		log.Lvlf1("RandHound - done")
	case <-time.After(time.Second * 10):
		t.Fatal("RandHound – time out")
	}

}
