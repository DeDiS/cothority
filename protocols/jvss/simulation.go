package jvss

import (
	"github.com/BurntSushi/toml"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/monitor"
	"github.com/dedis/cothority/lib/sda"
)

func init() {
	// FIXME Protocol doesn't exists:
	sda.SimulationRegister("JVSS", NewSimulation)
	sda.ProtocolRegisterName("JVSS", func(node *sda.Node) (sda.ProtocolInstance, error) { return NewJVSSProtocolInstance(node) })
}

type Simulation struct {
	sda.SimulationBFTree
	// 0 - no check
	// 1 - check signatures at the end
	Checking int
}

func NewSimulation(config string) (sda.Simulation, error) {
	es := &Simulation{Checking: 1}
	_, err := toml.Decode(config, es)
	if err != nil {
		return nil, err
	}
	return es, nil
}

func (jv *Simulation) Setup(dir string, hosts []string) (
	*sda.SimulationConfig, error) {
	sc := &sda.SimulationConfig{}
	jv.CreateEntityList(sc, hosts, 2000)
	err := jv.CreateTree(sc)
	return sc, err
}

func (jv *Simulation) Run(config *sda.SimulationConfig) error {
	size := config.Tree.Size()
	dbg.Lvl2("Size is:", size, "rounds:", jv.Rounds)
	msg := []byte("Test message for JVSS simulation")

	node, err := config.Overlay.CreateNewNodeName("JVSS", config.Tree)
	if err != nil {
		return err
	}
	proto := node.ProtocolInstance().(*JVSSProtocol)
	//m := monitor.NewMeasure("longterm")
	// compute and measure long-term secret:
	proto.Start()
	//m.Measure()

	for round := 0; round < jv.Rounds; round++ {
		dbg.Lvl1("Starting round", round)

		// we only measure the signing process
		r := monitor.NewTimeMeasure("round")
		sig, err := proto.Sign(msg)
		if err != nil {
			dbg.Error("Couldn't create signature")
			return err
		}

		// see if we got a valid signature:
		if jv.Checking == 1 {
			err = proto.Verify(msg, sig)
			if err != nil {
				dbg.Error("Got invalid signature")
				return err
			}
			dbg.Lvl3("Signature is OK")
		}
		r.Record()
	}
	return nil
}
