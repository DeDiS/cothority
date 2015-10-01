// Outputting data: output to csv files (for loading into excel)
//   make a datastructure per test output file
//   all output should be in the test_data subdirectory
//
// connect with logging server (receive json until "EOF" seen or "terminating")
//   connect to websocket ws://localhost:8080/log
//   receive each message as bytes
//		 if bytes contains "EOF" or contains "terminating"
//       wrap up the round, output to test_data directory, kill deploy2deter
//
// for memstats check localhost:8080/d/server-0-0/debug/vars
//   parse out the memstats zones that we are concerned with
//
// different graphs needed rounds:
//   load on the x-axis: increase messages per round holding everything else constant
//			hpn=40 bf=10, bf=50
//
// latency on y-axis, timestamp servers on x-axis push timestampers as higher as possible
//
//
package deploy

import (
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	dbg "github.com/dedis/cothority/lib/debug_lvl"
	"math"
	"os"
	"strconv"
	"time"
)

// Configuration-variables
var deploy_config *Config
var deployP Platform
var nobuild bool = false
var port int = 8081

// time-per-round * DefaultRounds = 10 * 20 = 3.3 minutes now
// this leaves us with 7 minutes for test setup and tear-down
var DefaultRounds int = 1

func init() {
	deploy_config = NewConfig()
	deployP = NewPlatform()
}

// Constant Applications marker
const ShamirSign string = "schnorr_sign"
const CollSign string = "coll_sign"
const CollStamp string = "coll_stamp"
const CollVote string = "coll_vote"

type T struct {
	nmachs int
	hpn    int
	bf     int

	rate     int
	rounds   int
	failures int

	rFail       int
	fFail       int
	testConnect bool
	app         string
}

// nmachs, hpn, bf
// rate, rounds, failures
// rFail, fFail, testConnect, app
var StampTestSingle = []T{
	{0, 1, 2,
		30, 20, 0,
		0, 0, false, CollStamp},
}

var SignTestSingle = []T{
	{0, 8, 8, 30, 10, 0, 0, 0, false, CollSign},
}

var SignTestMulti = []T{
	{0, 1, 2, 30, 20, 0, 0, 0, false, CollSign},
	{0, 8, 8, 30, 20, 0, 0, 0, false, CollSign},
	{0, 32, 8, 30, 20, 0, 0, 0, false, CollSign},
	{0, 64, 16, 30, 20, 0, 0, 0, false, CollSign},
	{0, 128, 16, 30, 20, 0, 0, 0, false, CollSign},
	{0, 256, 16, 30, 20, 0, 0, 0, false, CollSign},
}

var SignTestMulti2 = []T{
	{0, 256, 16, 30, 20, 0, 0, 0, false, CollSign},
	{0, 512, 32, 30, 20, 0, 0, 0, false, CollSign},
	{0, 1024, 64, 30, 20, 0, 0, 0, false, CollSign},
}

var HostsTestSingle = []T{
	{3, 1, 8, 30, 20, 0, 0, 0, false, CollStamp},
}

var HostsTestShort = []T{
	{0, 1, 2, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 8, 4, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 32, 16, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 64, 16, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 128, 16, 30, 20, 0, 0, 0, false, CollStamp},
}
var SchnorrHostSingle = []T{
	{3, 1, 2, 30, 20, 0, 0, 0, false, ShamirSign},
}

func Start(destination string, nbld bool, build string, machines int) {
	deployP.Configure(deploy_config)
	nobuild = nbld
	deploy_config.Nmachs = machines

	deployP.Stop()

	if nobuild == false {
		deployP.Build(build)
	}

	dbg.Lvl1("Starting tests")
	DefaultRounds = 5
	//RunTests("shamir_single", SchnorrHostSingle)
	RunTests("stamp_single", HostsTestSingle)
	//RunTests("sign_test_single", SignTestSingle)
	//RunTests("sign_test_multi2", SignTestMulti2)
	//RunTests("sign_test_multi", SignTestMulti)
	//RunTests("hosts_test_single", HostsTestSingle)
	//RunTests("hosts_test_short", HostsTestShort)
	//RunTests("hosts_test", HostsTest)
	//RunTests("stamp_test_single", StampTestSingle)
	//RunTests("sign_test_multi", SignTestMulti)
	// test the testing framework
	//RunTests("vote_test_no_signing.csv", VTest)
	//RunTests("hosts_test", HostsTest)
	// t := FailureTests
	// RunTests("failure_test.csv", t)
	// RunTests("vote_test", VotingTest)
	// RunTests("failure_test", FailureTests)
	//RunTests("sign_test", SignTest)
	// t := FailureTests
	// RunTests("failure_test", t)
	// t = ScaleTest(10, 1, 100, 2)
	// RunTests("scale_test.csv", t)
	// how does the branching factor effect speed
	// t = DepthTestFixed(100)
	// RunTests("depth_test.csv", t)

	// load test the client
	// t = RateLoadTest(40, 10)
	// RunTests("load_rate_test_bf10.csv", t)
	// t = RateLoadTest(40, 50)
	// RunTests("load_rate_test_bf50.csv", t)
}

// RunTests runs the given tests and puts the output into the
// given file name. It outputs RunStats in a CSV format.
func RunTests(name string, ts []T) {
	if len(ts) == 0 {
		return
	}
	for i, _ := range ts {
		ts[i].nmachs = deploy_config.Nmachs
	}

	MkTestDir()
	rs := make([]Stats, len(ts))
	nTimes := 1
	stopOnSuccess := true
	var f *os.File
	// Write the header
	firstStat := GetStat(ts[0])
	f, err := os.OpenFile(TestFile(name), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0660)
	defer f.Close()
	if err != nil {
		log.Fatal("error opening test file:", err)
	}
	firstStat.WriteTo(f)
	err = firstStat.ServerCSVHeader()
	if err != nil {
		log.Fatal("error writing test file header:", err)
	}
	err = f.Sync()
	if err != nil {
		log.Fatal("error syncing test file:", err)
	}

	for i, t := range ts {
		// run test t nTimes times
		// take the average of all successful runs
		runs := make([]Stats, 0, nTimes)
		for r := 0; r < nTimes; r++ {
			stats, err := RunTest(t)
			if err != nil {
				log.Fatalln("error running test:", err)
			}
			if deployP.Stop() == nil {
				runs = append(runs, stats)
				if stopOnSuccess {
					break
				}
			} else {
				dbg.Lvl1("Error for test ", r, " : ", err)
			}
		}

		if len(runs) == 0 {
			dbg.Lvl1("unable to get any data for test:", t)
			continue
		}

		s, err := AverageStats(runs...)
		if err != nil {
			dbg.Fatal("Could not average stats for test ", i)
		}
		rs[i] = s
		rs[i].WriteTo(f)
		//log.Println(fmt.Sprintf("Writing to CSV for %d: %+v", i, rs[i]))
		err = rs[i].ServerCSV()
		if err != nil {
			log.Fatal("error writing data to test file:", err)
		}
		err = f.Sync()
		if err != nil {
			log.Fatal("error syncing data to test file:", err)
		}

		cl, err := os.OpenFile(
			TestFile("client_latency_"+name+"_"+strconv.Itoa(i)),
			os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0660)
		if err != nil {
			log.Fatal("error opening test file:", err)
		}
		defer cl.Close()
		rs[i].WriteTo(cl)
		err = rs[i].ClientCSVHeader()
		err = rs[i].ClientCSV()
		if err != nil {
			log.Fatal("error writing client latencies to file:", err)
		}
		err = cl.Sync()
		if err != nil {
			log.Fatal("error syncing data to latency file:", err)
		}

	}
}

// hpn, bf, nmsgsG
func RunTest(t T) (Stats, error) {
	// add timeout for 10 minutes?
	done := make(chan struct{})
	// get the right statistics for the test
	stats := GetStat(t)
	cfg := &Config{
		t.nmachs, deploy_config.Nloggers, t.hpn, t.bf,
		-1, t.rate, t.rounds, t.failures, t.rFail, t.fFail,
		deploy_config.Debug, deploy_config.RootWait, t.app, deploy_config.Suite}

	dbg.Lvl1("Running test with parameters", cfg)
	dbg.Lvl1("Failures percent is", t.failures)

	deployP.Configure(cfg)
	deployP.Deploy()
	err := deployP.Start()
	if err != nil {
		log.Fatal(err)
		return stats, nil
	}

	// give it a while to start up
	time.Sleep(10 * time.Second)

	go func() {
		Monitor(stats)
		deployP.Stop()
		dbg.Lvl2(fmt.Sprintf("Test complete: %+v", stats))
		done <- struct{}{}
	}()

	// timeout the command if it takes too long
	select {
	case <-done:
		if !stats.Valid() {
			return stats, errors.New(fmt.Sprintf("unable to get good data: %+v", stats))
		}
		return stats, nil
		/* No time out for the moment
		case <-time.After(5 * time.Minute):
			return rs, errors.New("timed out")
		*/
	}
}

// high and low specify how many milliseconds between messages
func RateLoadTest(hpn, bf int) []T {
	return []T{
		{0, hpn, bf, 5000, DefaultRounds, 0, 0, 0, false, CollStamp}, // never send a message
		{0, hpn, bf, 5000, DefaultRounds, 0, 0, 0, false, CollStamp}, // one per round
		{0, hpn, bf, 500, DefaultRounds, 0, 0, 0, false, CollStamp},  // 10 per round
		{0, hpn, bf, 50, DefaultRounds, 0, 0, 0, false, CollStamp},   // 100 per round
		{0, hpn, bf, 30, DefaultRounds, 0, 0, 0, false, CollStamp},   // 1000 per round
	}
}

func DepthTest(hpn, low, high, step int) []T {
	ts := make([]T, 0)
	for bf := low; bf <= high; bf += step {
		ts = append(ts, T{0, hpn, bf, 10, DefaultRounds, 0, 0, 0, false, CollStamp})
	}
	return ts
}

func DepthTestFixed(hpn int) []T {
	return []T{
		{0, hpn, 1, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
		{0, hpn, 2, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
		{0, hpn, 4, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
		{0, hpn, 8, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
		{0, hpn, 16, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
		{0, hpn, 32, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
		{0, hpn, 64, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
		{0, hpn, 128, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
		{0, hpn, 256, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
		{0, hpn, 512, 30, DefaultRounds, 0, 0, 0, false, CollStamp},
	}
}

func ScaleTest(bf, low, high, mult int) []T {
	ts := make([]T, 0)
	for hpn := low; hpn <= high; hpn *= mult {
		ts = append(ts, T{0, hpn, bf, 10, DefaultRounds, 0, 0, 0, false, CollStamp})
	}
	return ts
}

// nmachs=32, hpn=128, bf=16, rate=500, failures=20, root failures, failures
var FailureTests = []T{
	{0, 64, 16, 30, 50, 0, 0, 0, false, CollStamp},
	{0, 64, 16, 30, 50, 0, 5, 0, false, CollStamp},
	{0, 64, 16, 30, 50, 0, 10, 0, false, CollStamp},
	{0, 64, 16, 30, 50, 5, 0, 5, false, CollStamp},
	{0, 64, 16, 30, 50, 5, 0, 10, false, CollStamp},
	{0, 64, 16, 30, 50, 5, 0, 10, true, CollStamp},
}

var VotingTest = []T{
	{0, 64, 16, 30, 50, 0, 0, 0, true, CollStamp},
	{0, 64, 16, 30, 50, 0, 0, 0, false, CollStamp},
}

func FullTests() []T {
	var nmachs = []int{1, 16, 32}
	var hpns = []int{1, 16, 32, 128}
	var bfs = []int{2, 4, 8, 16, 128}
	var rates = []int{5000, 500, 100, 30}
	failures := 0

	var tests []T
	for _, nmach := range nmachs {
		for _, hpn := range hpns {
			for _, bf := range bfs {
				for _, rate := range rates {
					tests = append(tests, T{nmach, hpn, bf, rate, DefaultRounds, failures, 0, 0, false, CollStamp})
				}
			}
		}
	}

	return tests
}

var HostsTest = []T{
	{0, 1, 2, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 2, 3, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 4, 3, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 8, 8, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 16, 16, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 32, 16, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 64, 16, 30, 20, 0, 0, 0, false, CollStamp},
	{0, 128, 16, 30, 50, 0, 0, 0, false, CollStamp},
}

var SignTest = []T{
	{0, 1, 2, 30, 20, 0, 0, 0, false, CollSign},
	{0, 2, 3, 30, 20, 0, 0, 0, false, CollSign},
	{0, 4, 3, 30, 20, 0, 0, 0, false, CollSign},
	{0, 8, 8, 30, 20, 0, 0, 0, false, CollSign},
	{0, 16, 16, 30, 20, 0, 0, 0, false, CollSign},
	{0, 32, 16, 30, 20, 0, 0, 0, false, CollSign},
	{0, 64, 16, 30, 20, 0, 0, 0, false, CollSign},
	{0, 128, 16, 30, 50, 0, 0, 0, false, CollSign},
}

var VTest = []T{
	{0, 1, 3, 10000000, 20, 0, 0, 0, false, CollVote},
	{0, 2, 4, 10000000, 20, 0, 0, 0, false, CollVote},
	{0, 4, 6, 10000000, 20, 0, 0, 0, false, CollVote},
	{0, 8, 8, 10000000, 20, 0, 0, 0, false, CollVote},
	{0, 16, 16, 10000000, 20, 0, 0, 0, false, CollVote},
	{0, 32, 16, 10000000, 20, 0, 0, 0, false, CollVote},
	{0, 64, 16, 10000000, 20, 0, 0, 0, false, CollVote},
	{0, 128, 16, 10000000, 20, 0, 0, 0, false, CollVote},
}

func MkTestDir() {
	err := os.MkdirAll("test_data/", 0777)
	if err != nil {
		log.Fatal("failed to make test directory")
	}
}

func TestFile(name string) string {
	return "test_data/" + name + ".csv"
}

func isZero(f float64) bool {
	return math.Abs(f) < 0.0000001
}
