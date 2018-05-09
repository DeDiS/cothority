package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/dedis/cothority.v2"
	"gopkg.in/dedis/kyber.v2"
	"gopkg.in/dedis/kyber.v2/sign/eddsa"
	"gopkg.in/dedis/kyber.v2/util/key"
	"gopkg.in/dedis/kyber.v2/util/random"
	"gopkg.in/dedis/onet.v2"
	"gopkg.in/dedis/onet.v2/log"
	"gopkg.in/dedis/onet.v2/network"
)

var tSuite = cothority.Suite

func TestClient_VerifyLink(t *testing.T) {
	l := onet.NewTCPTest(cothority.Suite)
	servers, roster, _ := l.GenTree(1, true)
	defer l.CloseAll()
	addr := roster.List[0].Address
	services := l.GetServices(servers, onet.ServiceFactory.ServiceID(Name))
	service := services[0].(*Service)
	c := NewClient()
	kp := key.NewKeyPair(cothority.Suite)

	err := c.VerifyLink(addr, kp.Public)
	require.NotNil(t, err)
	err = c.PinRequest(addr, "", kp.Public)
	require.NotNil(t, err)
	err = c.PinRequest(addr, service.data.Pin, kp.Public)
	require.Nil(t, err)
	err = c.VerifyLink(addr, kp.Public)
	require.Nil(t, err)
}

func TestFinalStatement_ToToml(t *testing.T) {
	pk := key.NewKeyPair(tSuite)
	si := network.NewServerIdentity(pk.Public, network.NewAddress(network.PlainTCP, "0:2000"))
	roster := onet.NewRoster([]*network.ServerIdentity{si})
	fs := &FinalStatement{
		Desc: &PopDesc{
			Name:     "test",
			DateTime: "yesterday",
			Roster:   roster,
		},
		Attendees: []kyber.Point{pk.Public},
	}
	fs.Signature = fs.Desc.Hash()
	fsStr, err := fs.ToToml()
	log.ErrFatal(err)
	log.Lvlf2("%x", fsStr)
	fs2, err := NewFinalStatementFromToml([]byte(fsStr))
	log.ErrFatal(err)
	require.Equal(t, fs.Desc.DateTime, fs2.Desc.DateTime)
	require.True(t, fs.Desc.Roster.Aggregate.Equal(fs2.Desc.Roster.Aggregate))
	require.True(t, fs.Attendees[0].Equal(fs2.Attendees[0]))
}

func TestFinalStatement_Verify(t *testing.T) {
	eddsa := eddsa.NewEdDSA(random.New())
	si := network.NewServerIdentity(eddsa.Public, network.NewAddress(network.PlainTCP, "0:2000"))
	roster := onet.NewRoster([]*network.ServerIdentity{si})
	fs := &FinalStatement{
		Desc: &PopDesc{
			Name:     "test",
			DateTime: "yesterday",
			Roster:   roster,
		},
		Attendees: []kyber.Point{eddsa.Public},
	}
	require.NotNil(t, fs.Verify())
	h, err := fs.Hash()
	log.ErrFatal(err)
	fs.Signature, err = eddsa.Sign(h)
	log.ErrFatal(err)
	require.Nil(t, fs.Verify())
	fs.Attendees = append(fs.Attendees, eddsa.Public)
	require.NotNil(t, fs.Verify())
}
