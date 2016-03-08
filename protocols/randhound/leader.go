package randhound

import (
	"bytes"
	"encoding/binary"
	"time"

	"github.com/dedis/crypto/poly"
	"github.com/dedis/crypto/random"
)

type Leader struct {
	Session *Session         // Session parameter block
	SID     []byte           // Session fingerprint
	Group   *Group           // Group parameter block
	GID     []byte           // Group fingerprint
	Rc      []byte           // Leader's trustee-selection random value
	Rs      [][]byte         // Peers' trustee-selection random values
	i1      I1               // I1 message sent to the peers
	i2      I2               // I2 - " -
	i3      I3               // I3 - " -
	i4      I4               // I4 - " -
	r1      []R1             // R1 messages received from the peers
	r2      []R2             // R2 - " -
	r3      []R3             // R3 - " -
	r4      []R4             // R4 - " -
	nr1     int              // Number of R1 messages received
	nr2     int              // Number of R2 messages received
	nr3     int              // Number of R3 messages received
	nr4     int              // Number of R4 messages received
	deals   []poly.Deal      // Unmarshaled deals from peers
	shares  []poly.PriShares // Revealed shares
}

func (rh *RandHound) newLeader() (*Leader, error) {

	// Choose client's trustee-selection randomness
	hs := rh.Node.Suite().Hash().Size()
	rc := make([]byte, hs)
	random.Stream.XORKeyStream(rc, rc)

	// Setup session
	//purpose := <-Purpose
	session, sid, err := rh.newSession(rh.Purpose)
	if err != nil {
		return nil, err
	}

	// Setup group
	group, gid, err := rh.newGroup()
	if err != nil {
		return nil, err
	}

	return &Leader{
		Session: session,
		SID:     sid,
		Group:   group,
		GID:     gid,
		Rc:      rc,
		r1:      make([]R1, len(rh.PID)),
		r2:      make([]R2, len(rh.PID)),
		r3:      make([]R3, len(rh.PID)),
		r4:      make([]R4, len(rh.PID)),
		deals:   make([]poly.Deal, len(rh.PID)),
		shares:  make([]poly.PriShares, len(rh.PID)),
	}, nil
}

func (rh *RandHound) newSession(purpose string) (*Session, []byte, error) {

	pub, err := rh.Node.Entity().Public.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}
	t := time.Now()
	tm, err := t.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}

	return &Session{
		LPubKey: pub,
		Purpose: purpose,
		Time:    t}, rh.Hash(pub, []byte(purpose), tm), nil
}

func (rh *RandHound) newGroup() (*Group, []byte, error) {

	np := len(rh.PID) // Number of peers
	nt := rh.N        // Number of trustees (= shares)
	buf := new(bytes.Buffer)
	ppub := make([][]byte, np)
	gp := [4]int{
		np / 3,
		np - (np / 3),
		nt,
		(nt + 1) / 2,
	} // Group parameters: F, L, K, T; TODO: sync up notations with rh.T, rh.R, rh.N!

	// Include public keys of all peers
	for i, pkey := range rh.PKeys {
		pub, err := pkey.MarshalBinary()
		if err != nil {
			return nil, nil, err
		}
		err = binary.Write(buf, binary.LittleEndian, pub)
		if err != nil {
			return nil, nil, err
		}
		ppub[i] = pub
	}

	// Process group parameters
	for _, g := range gp {
		err := binary.Write(buf, binary.LittleEndian, uint32(g))
		if err != nil {
			return nil, nil, err
		}
	}

	return &Group{
		PPubKey: ppub,
		F:       gp[0],
		L:       gp[1],
		K:       gp[2],
		T:       gp[3]}, rh.Hash(buf.Bytes()), nil
}
