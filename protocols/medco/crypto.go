package medco

import (
	"github.com/dedis/crypto/abstract"
	_"github.com/dedis/crypto/random"
	"errors"
	"fmt"
	"github.com/dedis/crypto/random"
)


const MAX_HOMOMORPHIC_INT int64 = 300
var PointToInt map[string]int64 = make(map[string]int64, MAX_HOMOMORPHIC_INT)
var currentGreatestM abstract.Point
var currentGreatestInt int64 = 0

type CipherText struct {
	//Suite abstract.Suite
	K, C  abstract.Point
}

type DeterministCipherText struct {
	C abstract.Point
}

type CipherVector []CipherText

func (c *CipherText) ReplaceContribution(suite abstract.Suite, prikey abstract.Secret, shortTermPriKey abstract.Secret) {
	egContrib := suite.Point().Mul(c.K, prikey)
	phContrib := suite.Point().Mul(suite.Point().Base(), shortTermPriKey)

	c.C.Sub(c.C, egContrib)
	c.C.Add(c.C, phContrib)
}

func (c *CipherText) Add(c1, c2 CipherText) *CipherText {
	c.C.Add(c1.C,c2.C)
	c.K.Add(c1.K,c2.K)
	return c
}

func (dc *DeterministCipherText) Equals(dc2 *DeterministCipherText) bool {
	return dc2.C.Equal(dc.C)
}

func (cv *CipherVector) Add(cv1, cv2 CipherVector) error{
	if len(cv1) != len(cv2) {
		return errors.New("Cannot add CipherVectors of different lenght.")
	}
	var i int
	for i, _ = range cv1 {
		(*cv)[i].Add(cv1[i], cv2[i])
	}
	return  nil
}

func (c *CipherText) SwitchForKey(suite abstract.Suite, private abstract.Secret, originalEphemKey, newKey abstract.Point, randomnessContribution abstract.Secret) {
	ephemKeyContrib := suite.Point().Mul(suite.Point().Base(), randomnessContribution)
	oldBlindingContrib := suite.Point().Mul(originalEphemKey, private)
	newBlindingContrib := suite.Point().Mul(newKey, randomnessContribution)

	c.K.Add(c.K, ephemKeyContrib)
	c.C.Sub(c.C, oldBlindingContrib)
	c.C.Add(c.C, newBlindingContrib)
}

func (cv *CipherVector) SwitchForKey(suite abstract.Suite, private abstract.Secret, originalEphemKeys []abstract.Point, newKey abstract.Point, randomnessContribution abstract.Secret){
	for i,c := range *cv {
		c.SwitchForKey(suite,private, originalEphemKeys[i],newKey,randomnessContribution)
	}
}

func InitCipherVector(suite abstract.Suite, dim int) *CipherVector {
	cv := make(CipherVector, dim)
	for i := 0; i < dim; i++ {
		cv[i] = CipherText{suite.Point().Null(), suite.Point().Null()}
	}
	return &cv
}

func NullCipherVector(suite abstract.Suite, dim int, pubkey abstract.Point) CipherVector {
	nv := make(CipherVector, dim)
	for i := 0 ; i<dim ; i++ {
		nv[i] = *EncryptInt(suite, pubkey, 0)
	}
	return nv
}

func EncryptBytes(suite abstract.Suite, pubkey abstract.Point, message []byte) (*CipherText, error) {

	// Embed the message into a curve point.
	// As we want to compare the encrypted points, we take a non-random stream
	M, remainder := suite.Point().Pick(message, suite.Cipher([]byte("HelloWorld")))

	if len(remainder) > 0 {
		return &CipherText{nil,nil},
		errors.New(fmt.Sprintf("Message too long: %s (%d bytes too long).",string(message), len(remainder)))
	}

	return EncryptPoint(suite, pubkey, M), nil
}

func EncryptInt(suite abstract.Suite, pubkey abstract.Point, integer int64) *CipherText {

	B := suite.Point().Base()
	i := suite.Secret().SetInt64(integer)
	M := suite.Point().Mul(B, i)

	return EncryptPoint(suite, pubkey, M)
}

func EncryptIntArray(suite abstract.Suite, pubkey abstract.Point, intArray []int64) *CipherVector {
	cv := make(CipherVector, len(intArray))
	for i, n := range intArray {
		cv[i] = *EncryptInt(suite,pubkey, n)
	}
	return &cv
}

func EncryptPoint(suite abstract.Suite, pubkey abstract.Point, M abstract.Point) *CipherText {
	B := suite.Point().Base()
	k := suite.Secret().Pick(random.Stream) // ephemeral private key
	// ElGamal-encrypt the point to produce ciphertext (K,C).
	K := suite.Point().Mul(B, k)       // ephemeral DH public key
	S := suite.Point().Mul(pubkey, k) // ephemeral DH shared secret
	C := S.Add(S, M)                  // message blinded with secret
	return &CipherText{K, C}
}

func DecryptInt(suite abstract.Suite, prikey abstract.Secret, cipher CipherText) int64 {

	S := suite.Point().Mul(cipher.K, prikey) // regenerate shared secret
	M := suite.Point().Sub(cipher.C, S)     // use to un-blind the message

	B := suite.Point().Base()
	var Bi abstract.Point
	var m int64
	var ok bool

	if m, ok = PointToInt[M.String()]; ok {
		return m
	}

	if (currentGreatestInt == 0) {
		currentGreatestM = suite.Point().Null()
	}

	for Bi, m = currentGreatestM, currentGreatestInt; !Bi.Equal(M) && m < MAX_HOMOMORPHIC_INT; Bi, m = Bi.Add(Bi, B), m+1 {
		PointToInt[Bi.String()] = m
	}
	currentGreatestM = Bi
	PointToInt[Bi.String()] = m
	currentGreatestInt = m
	return m
}

func DecryptIntVector(suite abstract.Suite, prikey abstract.Secret, cipherVector CipherVector) []int64 {
	result := make([]int64, len(cipherVector))
	for i, c := range cipherVector {
		result[i] = DecryptInt(suite, prikey, c)
	}
	return result
}


//func (c *CipherText) Aggregate(ag1, ag2 Aggregatable) error {
//		c1, ok1 := ag1.(*CipherText)
//		c2, ok2 := ag2.(*CipherText)
//		if ok1 && ok2 {
//			c.Add(*c1, *c2)
//			return nil
//		} else {
//
//			return errors.New("Cannot aggregate " + reflect.TypeOf(ag1).String() + " and " + reflect.TypeOf(ag2).String())
//		}
//}
//func (cv *CipherVector) Aggregate(ag1, ag2 Aggregatable) error {
//	cv1, ok1 := ag1.(*CipherVector)
//	cv2, ok2 := ag2.(*CipherVector)
//	if ok1 && ok2 {
//		cv.Add(*cv1, *cv2)
//		return nil
//	} else {
//
//		return errors.New("Cannot aggregate " + reflect.TypeOf(ag1).String() + " and " + reflect.TypeOf(ag2).String())
//	}
//}

//func (c * CipherText) MarshalBinary() (data []byte, err error) {
//	pointSize := (*c).Suite.PointLen()
//	data = make([]byte, 0, 2*pointSize)
//	b1,_ := c.K.MarshalBinary()
//	b2,_ := c.C.MarshalBinary()
//	data = append(data, b1...)
//	data = append(data, b2...)
//	return
//}
//
//func (c *CipherText) UnmarshalBinary(data []byte) error {
//	pointSize := (*c).Suite.PointLen()
//	c.K.UnmarshalBinary(data[:pointSize])
//	c.C.UnmarshalBinary(data[pointSize:])
//	return nil
//}
//
//func (c *CipherText) MarshalSize() int {
//	return 2 * c.Suite.PointLen()
//}
//
//func (c *CipherText) MarshalTo(w io.Writer) (int, error) {
//	b,_ := c.MarshalBinary()
//	return w.Write(b)
//}
//
//func (c *CipherText) UnmarshalFrom(r io.Reader) (int, error) {
//	buf := make([]byte, 2*c.Suite.PointLen())
//	n, err := io.ReadFull(r, buf)
//	if err != nil {
//		return n, err
//	}
//	return n, c.UnmarshalBinary(buf)
//}
//
//func (cv *CipherVector) MarshalBinary() (data []byte, err error) {
//	pointSize := (*cv)[0].suite.PointLen()
//	data = make([]byte, 2, 2+len(cv)*2*pointSize)
//	binary.BigEndian.PutUint16(data, len(cv))
//	for _, c := range *cv {
//		data = append(data, c.MarshalBinary()...)
//	}
//	return
//}
//
//func (cv *CipherVector) UnmarshalBinary(data []byte) error {
//	pointSize := (*cv)[0].suite.PointLen()
//	ciphSize := 2*pointSize
//	numEl := binary.BigEndian.Uint16(data)
//	data = data[2:]
//	*cv = make(CipherVector, numEl)
//	for _, c := range *cv {
//		c.UnmarshalBinary(data[:ciphSize])
//		data = data[ciphSize:]
//	}
//	return
//}