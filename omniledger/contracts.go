package omniledger

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/dedis/cothority"
	bc "github.com/dedis/cothority/byzcoin"
	"github.com/dedis/cothority/darc"
	lib "github.com/dedis/cothority/omniledger/lib"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"github.com/dedis/protobuf"
	"time"
)

// ValidTimeWindow indicates the maximum difference of time allowed between an instruction timestamp and a node's internal clock.
// The motivation here is to prevent attacks where adversary tamper with the instruction timestamp.
// In future work, the valid time window should be made a parameter of the omniledger, just like the epoch size, instead of a hardcoded constant.
//
// Example:
// A new epoch can only be called N seconds after the previous new epoch (where N is a configuration parameter of the omniledger).
// The contract checks if the instruction timestamp respects this constraints.
// An adversary might change the instrutction timestamp such that it bypasses this constraint.
// To prevent this, an extra constraint on the instruction timestamp is added: the timestamp must also be within
// ValidTimeWindow seconds of a node's internal clock.
const ValidTimeWindow = time.Second * 60

// ContractOmniledgerEpochID denotes a omniledger epoch contract
var ContractOmniledgerEpochID = "omniledgerepoch"

type contractOmniledgerEpoch struct {
	bc.BasicContract
	lib.ChainConfig
}

func contractOmniledgerEpochFromBytes(in []byte) (bc.Contract, error) {
	c := &contractOmniledgerEpoch{}
	err := protobuf.DecodeWithConstructors(in, &c.ChainConfig, network.DefaultConstructors(cothority.Suite))
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *contractOmniledgerEpoch) Spawn(rst bc.ReadOnlyStateTrie, inst bc.Instruction, coins []bc.Coin) (sc []bc.StateChange, cout []bc.Coin, err error) {
	cout = coins

	// Decode darc and do some verifications
	darcBuf := inst.Spawn.Args.Search("darc")
	d, err := darc.NewFromProtobuf(darcBuf)
	if err != nil {
		log.Error("couldn't decode darc")
		return
	}
	if d.Rules.Count() == 0 {
		err = errors.New("don't accept darc with empty rules")
		return
	}
	if err = d.Verify(true); err != nil {
		log.Error("couldn't verify darc")
		return
	}

	//Decode instruction's arguments
	shardCountBuf := inst.Spawn.Args.Search("shardCount")
	shardCountDecoded, err := binary.ReadVarint(bytes.NewBuffer(shardCountBuf))
	if err != nil {
		log.Error("couldn't decode shard count")
		return
	}
	shardCount := int(shardCountDecoded)

	epochSizeBuf := inst.Spawn.Args.Search("epochSize")
	epochSize, err := lib.DecodeDuration(epochSizeBuf)
	if err != nil {
		log.Error("couldn't decode epoch size")
		return
	}

	tsBuf := inst.Spawn.Args.Search("timestamp")
	ts := time.Unix(int64(binary.BigEndian.Uint64(tsBuf)), 0)

	// Verify instruction's timestamp is not too different from the node's clock
	if !checkValidTime(ts, ValidTimeWindow) {
		err = errors.New("Client timestamp is too different from node's clock")
		return
	}

	// Get initial roster from instruction's arguments
	rosterBuf := inst.Spawn.Args.Search("roster")
	roster := &onet.Roster{}
	err = protobuf.DecodeWithConstructors(rosterBuf, roster, network.DefaultConstructors(cothority.Suite))
	if err != nil {
		log.Error("Error while decoding constructors")
		return
	}

	// Do sharding
	shardRosters := lib.Sharding(roster, shardCount, int64(binary.BigEndian.Uint64(inst.DeriveID("").Slice())))

	// Create ChainConfig struct to store instance data
	config := &lib.ChainConfig{
		Roster:       roster,
		ShardCount:   shardCount,
		EpochSize:    epochSize,
		Timestamp:    ts,
		ShardRosters: shardRosters,
	}

	// Encode the config
	configBuf, err := protobuf.Encode(config)
	if err != nil {
		return
	}

	// Return state changes
	darcID := d.GetBaseID()
	sc = []bc.StateChange{
		bc.NewStateChange(bc.Create, inst.DeriveID(""), ContractOmniledgerEpochID, configBuf, darcID),
	}

	return
}

func (c *contractOmniledgerEpoch) Invoke(rst bc.ReadOnlyStateTrie, inst bc.Instruction, coins []bc.Coin) (sc []bc.StateChange, cout []bc.Coin, err error) {
	cout = coins

	switch inst.Invoke.Command {
	case "request_new_epoch":
		// Decode instrution's arguments
		tsBuf := inst.Invoke.Args.Search("timestamp")
		ts := time.Unix(int64(binary.BigEndian.Uint64(tsBuf)), 0)

		// Verify instruction's timestamp is not too different from the node's clock
		if !checkValidTime(ts, ValidTimeWindow) {
			err = errors.New("Client timestamp is too different from node's clock")
			return
		}

		// Get and decode instance data
		var buf []byte
		var darcID darc.ID
		buf, _, _, darcID, err = rst.GetValues(inst.InstanceID.Slice())
		if err != nil {
			return
		}

		cc := &lib.ChainConfig{}
		if buf != nil {
			err = protobuf.DecodeWithConstructors(buf, cc, network.DefaultConstructors(cothority.Suite))
			if err != nil {
				return
			}
		}

		// Verify it is indeed time for a new epoch
		if ts.Sub(cc.Timestamp).Seconds() >= cc.EpochSize.Seconds() {
			// compute new shards
			seed := int64(binary.BigEndian.Uint64(inst.DeriveID("").Slice()))

			shardRosters := lib.Sharding(cc.Roster, cc.ShardCount, seed)
			log.Print("AFTER SHARDING:", shardRosters[0].List, shardRosters[1].List)

			// update chain config
			cc.Timestamp = ts
			cc.ShardRosters = shardRosters
			var ccBuf []byte
			ccBuf, err = protobuf.Encode(cc)
			if err != nil {
				return
			}

			// return changes
			sc = []bc.StateChange{
				bc.NewStateChange(bc.Update, inst.InstanceID, ContractOmniledgerEpochID, ccBuf, darcID),
			}
			log.Print("UPDATED ID BYZCOIN")

			return
		}

		return nil, coins, errors.New("Request new epoch failed, was called too soon")
	default:
		err = errors.New("unknown instruction type")
		return
	}
}

func checkValidTime(t time.Time, window time.Duration) bool {
	diff := time.Since(t)
	if diff < 0 {
		diff *= -1
	}

	return diff.Seconds() <= window.Seconds()
}
