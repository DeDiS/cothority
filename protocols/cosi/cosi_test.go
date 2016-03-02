package cosi

import (
	"github.com/dedis/cothority/lib/cosi"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/sda"
	"github.com/dedis/cothority/lib/testutil"
	"github.com/dedis/crypto/abstract"
	"testing"
	"time"
)

func TestCosi(t *testing.T) {
	defer testutil.AfterTest(t)
	dbg.TestOutput(testing.Verbose(), 4)

	local := sda.NewLocalTest()

	hosts, el, tree := local.GenBigTree(3, 3, 1, true, true)
	defer local.CloseAll()

	done := make(chan bool)
	// create the message we want to sign for this round
	msg := []byte("Hello World Cosi")

	// Register the function generating the protocol instance
	var root *ProtocolCosi
	// function that will be called when protocol is finished by the root
	doneFunc := func(chal abstract.Secret, resp abstract.Secret) {
		suite := hosts[0].Suite()
		aggPublic := suite.Point().Null()
		for _, e := range el.List {
			aggPublic = aggPublic.Add(aggPublic, e.Public)
		}
		if err := root.Cosi.VerifyResponses(aggPublic); err != nil {
			t.Fatal("Error verifying responses", err)
		}
		if err := cosi.VerifySignature(suite, msg, aggPublic, chal, resp); err != nil {
			t.Fatal("error verifying signature:", err)
		}
		done <- true
	}
	fn := func(node *sda.Node) (sda.ProtocolInstance, error) {
		if root == nil {
			var err error
			root, err = NewRootProtocolCosi(msg, node)
			root.RegisterDoneCallback(doneFunc)
			return root, err
		}
		return NewProtocolCosi(node)
	}

	sda.ProtocolRegisterName("ProtocolCosi", fn)
	// Start the protocol
	_, err := local.StartNewNodeName("ProtocolCosi", tree)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		return
	case <-time.After(time.Second * 2):
		t.Fatal("Could not get signature verification done in time")
	}
}
