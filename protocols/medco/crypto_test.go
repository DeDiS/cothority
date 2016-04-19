package medco_test

import (
	"testing"
	"github.com/dedis/crypto/edwards"
	"github.com/dedis/crypto/random"
	"github.com/dedis/cothority/protocols/medco"
	"reflect"
	"github.com/dedis/crypto/abstract"
	"github.com/dedis/cothority/lib/dbg"
)

var suite = edwards.NewAES128SHA256Ed25519(false)

func genKeys() (secKey abstract.Secret, pubKey abstract.Point) {
	secKey = suite.Secret().Pick(random.Stream)
	pubKey = suite.Point().Mul(suite.Point().Base(), secKey)
	return
}

func TestNullCipherText(t *testing.T) {

	secKey, pubKey := genKeys()

	nullEnc := medco.EncryptInt(suite, pubKey, 0)
	nullDec := medco.DecryptInt(suite, secKey, *nullEnc)

	if (0 != nullDec) {
		t.Fatal("Decryption of encryption of 0 should be 0, got", nullDec)
	}

	var twoTimesNullEnc = medco.CipherText{suite.Point().Null(), suite.Point().Null()}
	twoTimesNullEnc.Add(*nullEnc, *nullEnc)
	twoTimesNullDec := medco.DecryptInt(suite, secKey, twoTimesNullEnc)

	if (0 != nullDec) {
		t.Fatal("Decryption of encryption of 0+0 should be 0, got", twoTimesNullDec)
	}

}

func TestNullCipherVector(t *testing.T) {
	dbg.SetDebugVisible(3)
	secKey, pubKey := genKeys()

	nullVectEnc := medco.NullCipherVector(suite, 10, pubKey)

	nullVectDec := medco.DecryptIntVector(suite, secKey, nullVectEnc)

	target := []int64{0,0,0,0,0,0,0,0,0,0}
	if (!reflect.DeepEqual(nullVectDec, target)) {
		t.Fatal("Null vector of dimension 4 should be ",target, "got", nullVectDec )
	}

	twoTimesNullEnc := medco.InitCipherVector(suite, 10)
	err := twoTimesNullEnc.Add(nullVectEnc, nullVectEnc)
	twoTimesNullDec := medco.DecryptIntVector(suite,secKey,*twoTimesNullEnc)

	if (!reflect.DeepEqual(twoTimesNullDec, target)) {
		t.Fatal("Null vector + Null vector should be ",target, "got", twoTimesNullDec )
	}
	if (err != nil) {
		t.Fatal("No error should be produced, got", err)
	}
}


