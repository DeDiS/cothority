package randhoundco

import (
	"crypto/rand"

	"github.com/BurntSushi/toml"
	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/monitor"
	"github.com/dedis/cothority/network"
	"github.com/dedis/cothority/sda"
)

func init() {
	sda.SimulationRegister("Randhoundco", NewSimulation)
}

// Simulation implements a JVSS simulation
type Simulation struct {
	sda.SimulationBFTree
	// number of JVSS groups randhoundco will generate
	Groups int
}

// NewSimulation creates a Randhoundco simulation
func NewSimulation(config string) (sda.Simulation, error) {
	r := &Simulation{}
	_, err := toml.Decode(config, r)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Setup creates the tree used by the randhoundco simulation.
func (r *Simulation) Setup(dir string, hosts []string) (*sda.SimulationConfig, error) {
	sim := new(sda.SimulationConfig)
	r.CreateRoster(sim, hosts, 2000)
	err := r.CreateTree(sim)
	return sim, err
}

// Run starts the simulation.
func (r *Simulation) Run(config *sda.SimulationConfig) error {
	var msg = []byte("Hello World")
	// creating the groups requests
	req, leaderRoster := CreateGroups(config.Roster, r.Groups)
	leaderTree := leaderRoster.GenerateBinaryTree()

	tni := config.Overlay.NewTreeNodeInstanceFromProtoName(leaderTree, FullProto)
	p, err := NewRootProtocol(tni, req)
	if err != nil {
		log.Error(err)
		return err
	}
	err = config.Overlay.RegisterProtocolInstance(p)
	if err != nil {
		log.Error(err)
		return err
	}

	setupM := monitor.NewTimeMeasure("setup")
	// wait for the setup the setup
	log.Lvl1("Starting setup of randhoundco")
	p.Start()
	setupM.Record()

	full := p.(*fullProto)
	// launch the randomness
	for round := 0; round < r.Rounds; round++ {
		log.Lvl1("Starting randomness round ", round)
		roundM := monitor.NewTimeMeasure("round")
		_, err := full.NewRound(msg)
		if err != nil {
			log.Error(err)
			return err
		}
		roundM.Record()
	}
	return nil
}

// CreateGroups takes a roster and a number of groups to generate. It create the
// groups and returns the GroupRequests containing those groups. It also returns
// the Roster to use by all the JVSS leaders on the "main" tree.
func CreateGroups(r *sda.Roster, nbGroup int) (GroupRequests, *sda.Roster) {
	var list = r.List
	var shard []*network.ServerIdentity
	var groups []GroupRequest
	n := len(list) / nbGroup
	// add the client identity to the roster
	leaders := []*network.ServerIdentity{}
	var leadersIdx []int32
	for i := 0; i < len(list); i++ {
		shard = append(shard, list[i])
		if len(shard) == 1 {
			leaders = append(leaders, list[i])
			leadersIdx = append(leadersIdx, int32(len(groups)))
		}
		if (i%n == n-1) && len(groups) < nbGroup-1 {
			groups = append(groups, GroupRequest{shard})
			shard = []*network.ServerIdentity{}
		}
	}
	groups = append(groups, GroupRequest{shard})
	// generate the random identifier
	// XXX This step will also be replaced by the randhound protocol's output
	// once merged.
	var id [16]byte
	n, err := rand.Read(id[:])
	if n != 16 || err != nil {
		panic("the whole system is compromised, leave the ship")
	}
	g := GroupRequests{id[:], groups, leadersIdx}
	roster := sda.NewRoster(leaders)
	return g, roster

}
