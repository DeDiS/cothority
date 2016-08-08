package medco

import (
	"github.com/BurntSushi/toml"
	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/monitor"
	"github.com/dedis/cothority/sda"
	"github.com/dedis/cothority/services/medco/libmedco"
	"github.com/dedis/crypto/random"
)

const switchedAttributesCount = 100

const switchedVectorCount = 10

func init() {
	sda.SimulationRegister("KeySwitching", NewKeySwitchingSimulation)

}

// KeySwitchingSimulation holds the state of a simulation.
type KeySwitchingSimulation struct {
	sda.SimulationBFTree
}

// NewKeySwitchingSimulation constructs a key switching simulation.
func NewKeySwitchingSimulation(config string) (sda.Simulation, error) {
	sim := &KeySwitchingSimulation{}
	_, err := toml.Decode(config, sim)

	if err != nil {
		return nil, err
	}
	return sim, nil
}

// Setup initializes the simulation.
func (sim *KeySwitchingSimulation) Setup(dir string, hosts []string) (*sda.SimulationConfig, error) {
	sc := &sda.SimulationConfig{}
	sim.CreateRoster(sc, hosts, 20)
	err := sim.CreateTree(sc)

	if err != nil {
		return nil, err
	}

	log.Lvl1("Setup done")

	return sc, nil
}

// Run starts the simulation.
func (sim *KeySwitchingSimulation) Run(config *sda.SimulationConfig) error {
	for round := 0; round < sim.Rounds; round++ {
		log.Lvl1("Starting round", round)
		rooti, err := config.Overlay.CreateProtocolSDA(config.Tree, "KeySwitching")
		if err != nil {
			return err
		}

		root := rooti.(*KeySwitchingProtocol)
		suite := root.Suite()
		aggregateKey := root.Roster().Aggregate

		ciphertexts := make(map[libmedco.TempID]libmedco.CipherVector)

		tab := make([]int64, switchedAttributesCount)
		for i := 0; i < switchedAttributesCount; i++ {
			tab[i] = int64(1)
		}

		for i := 0; i < switchedVectorCount; i++ {
			ciphertexts[libmedco.TempID(i)] = *libmedco.EncryptIntVector(aggregateKey, tab)
		}

		clientSecret := suite.Scalar().Pick(random.Stream)
		clientPublic := suite.Point().Mul(suite.Point().Base(), clientSecret)

		root.ProtocolInstance().(*KeySwitchingProtocol).TargetPublicKey = &clientPublic
		root.ProtocolInstance().(*KeySwitchingProtocol).TargetOfSwitch = &ciphertexts

		round := monitor.NewTimeMeasure("MEDCO_PROTOCOL")
		root.StartProtocol()
		<-root.ProtocolInstance().(*KeySwitchingProtocol).FeedbackChannel
		round.Record()

	}

	return nil
}
