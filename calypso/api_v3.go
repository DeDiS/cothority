package calypso

import (
	"go.dedis.ch/cothority/v3"
	"time"

	"go.dedis.ch/cothority/v3/byzcoin"
	"go.dedis.ch/cothority/v3/darc"
	"go.dedis.ch/cothority/v3/skipchain"
	"go.dedis.ch/onet/v3"
	"go.dedis.ch/onet/v3/network"
	"go.dedis.ch/protobuf"
)


// ClientV3 is a class to communicate to the calypso service.
type ClientV3 struct {
	bcClient *byzcoin.Client
	c        *onet.Client
	ltsReply *CreateLTSReply
}

// WriteReply is returned upon successfully spawning a Write instance.
type WriteReply struct {
	*byzcoin.AddTxResponse
	byzcoin.InstanceID
}

// ReadReply is is returned upon successfully spawning a Read instance.
type ReadReply struct {
	*byzcoin.AddTxResponse
	byzcoin.InstanceID
}

// NewClient instantiates a new ClientV3.
// It takes as input an "initialized" byzcoin ClientV3
// with an already created ledger
func NewClientV3(byzcoin *byzcoin.Client) *ClientV3 {
	return &ClientV3{bcClient: byzcoin, c: onet.NewClient(
		cothority.Suite, ServiceName)}
}

// CreateLTS creates a random LTSID that can be used to reference the LTS group
// created. It first sends a transaction to ByzCoin to spawn a LTS instance,
// then it asks the Calypso cothority to start the DKG.
func (c *ClientV3) CreateLTSV3(ltsRoster *onet.Roster, darcID darc.ID, signers []darc.Signer, counters []uint64) (reply *CreateLTSReply, err error) {
	// Make the transaction and get its proof
	buf, err := protobuf.Encode(&LtsInstanceInfo{*ltsRoster})
	if err != nil {
		return nil, err
	}
	inst := byzcoin.Instruction{
		InstanceID: byzcoin.NewInstanceID(darcID),
		Spawn: &byzcoin.Spawn{
			ContractID: ContractLongTermSecretID,
			Args: []byzcoin.Argument{
				{
					Name:  "lts_instance_info",
					Value: buf,
				},
			},
		},
		SignerCounter: counters,
	}
	tx := byzcoin.ClientTransaction{
		Instructions: []byzcoin.Instruction{inst},
	}
	if err := tx.FillSignersAndSignWith(signers...); err != nil {
		return nil, err
	}
	if _, err := c.bcClient.AddTransactionAndWait(tx, 4); err != nil {
		return nil, err
	}
	resp, err := c.bcClient.GetProof(tx.Instructions[0].DeriveID("").Slice())
	if err != nil {
		return nil, err
	}

	// Start the DKG
	reply = &CreateLTSReply{}
	err = c.c.SendProtobuf(c.bcClient.Roster.List[0], &CreateLTS{
		Proof: resp.Proof,
	}, reply)
	if err != nil {
		return nil, err
	}
	return reply, nil
}

// Authorise adds a ByzCoinID to the list of authorized IDs for each
// server in the roster. The AuthoriseByzcoinID service refuses requests
// that do not come from localhost.
//
// It should be called by the administrator at the beginning, before any other
// API calls are made. A ByzCoinID that is not authorised will not be allowed to
// call the other APIs.
func (c *ClientV3) AuthoriseV3(who *network.ServerIdentity, what skipchain.SkipBlockID) error {
	reply := &AuthoriseReply{}
	err := c.c.SendProtobuf(who, &Authorise{ByzCoinID: what}, reply)
	if err != nil {
		return err
	}
	return nil
}

// DecryptKey takes as input Read- and Write- Proofs. It verifies that
// the read/write requests match and then re-encrypts the secret
// given the public key information of the reader.
func (c *ClientV3) DecryptKeyV3(dkr *DecryptKey) (reply *DecryptKeyReply, err error) {
	reply = &DecryptKeyReply{}
	err = c.c.SendProtobuf(c.bcClient.Roster.List[0], dkr, reply)
	if err != nil {
		return nil, err
	}
	return reply, nil
}

// WaitProof calls the byzcoin client's wait proof
func (c *ClientV3) WaitProofV3(id byzcoin.InstanceID, interval time.Duration,
	value []byte) (*byzcoin.Proof, error) {
	return c.bcClient.WaitProof(id, interval, value)
}

// AddWrite creates a Write Instance by adding a transaction on the byzcoin client.
// Input:
//   - write - A Write structure
//   - signer - The data owner who will sign the transaction
//   - signerCtr - A monotonically increaing counter for every signer
//   - darc - The darc governing this instance
//   - wait - The number of blocks to wait -- 0 means no wait
//
// Output:
//   - reply - WriteReply containing the transaction response and instance id
//   - err - Error if any, nil otherwise.
func (c *ClientV3) AddWriteV3(write *Write, signer darc.Signer, signerCtr uint64,
	darc darc.Darc, wait int) (
	reply *WriteReply, err error) {
	reply = &WriteReply{}
	if err != nil {
		return nil, err
	}
	writeBuf, err := protobuf.Encode(write)
	if err != nil {
		return nil, err
	}
	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{{
			InstanceID: byzcoin.NewInstanceID(darc.GetBaseID()),
			Spawn: &byzcoin.Spawn{
				ContractID: ContractWriteID,
				Args: byzcoin.Arguments{{
					Name: "write", Value: writeBuf}},
			},
			SignerCounter: []uint64{signerCtr},
		}},
	}
	//Sign the transaction
	err = ctx.FillSignersAndSignWith(signer)
	if err != nil {
		return nil, err
	}
	reply.InstanceID = ctx.Instructions[0].DeriveID("")
	//Delegate the work to the byzcoin client
	reply.AddTxResponse, err = c.bcClient.AddTransactionAndWait(ctx, wait)
	if err != nil {
		return nil, err
	}
	return reply, err
}

// AddRead creates a Read Instance by adding a transaction on the byzcoin client.
// Input:
//   - proof - A ByzCoin proof of the Write Operation.
//   - signer - The data owner who will sign the transaction
//   - signerCtr - A monotonically increaing counter for every signer
//   - darc - The darc governing this instance
//   - wait - The number of blocks to wait -- 0 means no wait
//
// Output:
//   - reply - ReadReply containing the transaction response and instance id
//   - err - Error if any, nil otherwise.
func (c *ClientV3) AddReadV3(proof *byzcoin.Proof, signer darc.Signer, signerCtr uint64,
	darc darc.Darc, wait int) (
	reply *ReadReply, err error) {
	var readBuf []byte
	read := &Read{
		Write: byzcoin.NewInstanceID(proof.InclusionProof.Key()),
		Xc:    signer.Ed25519.Point,
	}
	reply = &ReadReply{}
	readBuf, err = protobuf.Encode(read)
	if err != nil {
		return nil, err
	}

	if err != nil {
		return nil, err
	}
	ctx := byzcoin.ClientTransaction{
		Instructions: byzcoin.Instructions{{
			InstanceID: byzcoin.NewInstanceID(proof.InclusionProof.Key()),
			Spawn: &byzcoin.Spawn{
				ContractID: ContractReadID,
				Args:       byzcoin.Arguments{{Name: "read", Value: readBuf}},
			},
			SignerCounter: []uint64{signerCtr},
		}},
	}
	err = ctx.FillSignersAndSignWith(signer)
	reply.InstanceID = ctx.Instructions[0].DeriveID("")
	if err != nil {
		return nil, err
	}
	reply.AddTxResponse, err = c.bcClient.AddTransactionAndWait(ctx, wait)
	if err != nil {
		return nil, err
	}
	return reply, nil
}

// SpawnDarc spawns a Darc Instance by adding a transaction on the byzcoin client.
// Input:
//   - signer - The signer authorizing the spawn of this darc (calypso "admin")
//   - signerCtr - A monotonically increaing counter for every signer
//   - controlDarc - The darc governing this spawning
//   - spawnDarc - The darc to be spawned
//   - wait - The number of blocks to wait -- 0 means no wait
//
// Output:
//   - reply - AddTxResponse containing the transaction response
//   - err - Error if any, nil otherwise.
func (c *ClientV3) SpawnDarcV3(signer darc.Signer, signerCtr uint64,
	controlDarc darc.Darc, spawnDarc darc.Darc, wait int) (
	reply *byzcoin.AddTxResponse, err error) {
	reply = &byzcoin.AddTxResponse{}
	if err != nil {
		return nil, err
	}
	darcBuf, err := spawnDarc.ToProto()
	if err != nil {
		return nil, err
	}

	ctx := byzcoin.ClientTransaction{
		Instructions: []byzcoin.Instruction{{
			InstanceID: byzcoin.NewInstanceID(controlDarc.GetBaseID()),
			Spawn: &byzcoin.Spawn{
				ContractID: byzcoin.ContractDarcID,
				Args: []byzcoin.Argument{{
					Name:  "darc",
					Value: darcBuf,
				}},
			},
			SignerCounter: []uint64{signerCtr},
		}},
	}
	err = ctx.FillSignersAndSignWith(signer)
	if err != nil {
		return nil, err
	}
	return c.bcClient.AddTransactionAndWait(ctx, wait)
}
