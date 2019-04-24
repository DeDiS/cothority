package asmsproto

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/pairing/bn256"
	"go.dedis.ch/kyber/v3/sign"
	"go.dedis.ch/kyber/v3/sign/asmbls"
	"go.dedis.ch/kyber/v3/sign/cosi"
	"go.dedis.ch/kyber/v3/util/random"
)

func TestAsmsSignature_Verify(t *testing.T) {
	msg := []byte("abc")
	suite := bn256.NewSuite()
	sk1, pk1 := asmbls.NewKeyPair(suite, random.New())
	sk2, pk2 := asmbls.NewKeyPair(suite, random.New())
	_, pk3 := asmbls.NewKeyPair(suite, random.New())

	mask, err := sign.NewMask(suite, []kyber.Point{pk1, pk2, pk3}, nil)
	require.NoError(t, err)
	mask.SetBit(0, true)
	mask.SetBit(1, true)

	sig1, err := asmbls.Sign(suite, sk1, msg)
	require.NoError(t, err)
	sig2, err := asmbls.Sign(suite, sk2, msg)
	require.NoError(t, err)

	asig, err := asmbls.AggregateSignatures(suite, [][]byte{sig1, sig2}, mask)
	require.NoError(t, err)

	buf, err := asig.MarshalBinary()
	require.NoError(t, err)

	sig := AsmsSignature(append(buf, mask.Mask()...))
	pubkeys := []kyber.Point{pk1, pk2, pk3}

	require.Error(t, sig.Verify(suite, msg, pubkeys))
	policy := cosi.NewThresholdPolicy(2)
	require.NoError(t, sig.VerifyWithPolicy(suite, msg, pubkeys, policy))
	require.Error(t, sig.VerifyWithPolicy(suite, []byte{}, pubkeys, policy))
}