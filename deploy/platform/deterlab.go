// Deterlab is responsible for setting up everything to test the application
// on deterlab.net
// Given a list of hostnames, it will create an overlay
// tree topology, using all but the last node. It will create multiple
// nodes per server and run timestamping processes. The last node is
// reserved for the logging server, which is forwarded to localhost:8081
//
// Creates the following directory structure:
// build/ - where all cross-compiled executables are stored
// remote/ - directory to be copied to the deterlab server
//
// The following apps are used:
//   deter - runs on the user-machine in deterlab and launches the others
//   logserver - runs on the first 'Loggers' servers - first is the master, rest are slaves
//   forkexec - runs on the other servers and launches the app, so it can measure its cpu usage

package platform

import (
	"os"
	"os/exec"
	"sync"

	"bufio"
	_ "errors"
	"fmt"
	"github.com/dedis/cothority/lib/app"
	"github.com/dedis/cothority/lib/cliutils"
	dbg "github.com/dedis/cothority/lib/debug_lvl"
	"github.com/dedis/cothority/lib/graphs"
	"io/ioutil"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Deterlab struct {
	// The login on the platform
	Login        string
	// The outside host on the platform
	Host         string
	// The name of the project
	Project      string
	// Name of the Experiment - also name of hosts
	Experiment   string
	// Directory of applications
	AppDir       string
	// Directory where everything is copied into
	DeployDir    string
	// Directory for building
	BuildDir     string
	// Working directory of deterlab
	DeterDir     string
	// Where the main logging machine resides
	MasterLogger string
	// DNS-resolvable names
	Phys         []string
	// VLAN-IP names
	Virt         []string

	// Which app to run
	App          string
	// Number of machines
	Machines     int
	// Number of loggers
	Loggers      int
	// Channel to communication stopping of experiment
	sshDeter     chan string
	// Whether the simulation is started
	started      bool
	// Debugging-level: 0 is none - 5 is everything
	Debug        int

	// All hostnames used concatenated with the port
	Hostnames    []string

	// Testing the connection?
	TestConnect  bool
}

func (d *Deterlab) Configure() {
	// Directory setup - would also be possible in /tmp
	pwd, _ := os.Getwd()
	d.DeterDir = pwd + "/platform/deterlab"
	d.DeployDir = d.DeterDir + "/remote"
	d.BuildDir = d.DeterDir + "/build"
	d.AppDir = pwd + "/../app"
	dbg.Lvl3("Dirs are:", d.DeterDir, d.DeployDir)
	dbg.Lvl3("Dirs are:", d.BuildDir, d.AppDir)
	d.LoadAndCheckDeterlabVars()

	d.Debug = dbg.DebugVisible
	if d.App == "" {
		dbg.Fatal("No app defined in simulation")
	}

	// Setting up channel
	d.sshDeter = make(chan string)
}

// build is the name of the app to build
// empty = all otherwise build specific package
func (d *Deterlab) Build(build string) error {
	dbg.Lvl1("Building for", d.Login, d.Host, d.Project, build)
	start := time.Now()

	var wg sync.WaitGroup

	// Start with a clean build-directory
	current, _ := os.Getwd()
	dbg.Lvl3("Current dir is:", current, d.DeterDir)
	defer os.Chdir(current)

	// Go into deterlab-dir and create the build-dir
	os.Chdir(d.DeterDir)
	os.RemoveAll(d.BuildDir)
	os.Mkdir(d.BuildDir, 0777)

	// start building the necessary packages
	packages := []string{"logserver", "forkexec", "app", "users"}
	if build != "" {
		packages = strings.Split(build, ",")
	}
	dbg.Lvl3("Starting to build all executables", packages)
	for _, p := range packages {
		src_dir := d.DeterDir + "/" + p
		basename := path.Base(p)
		if p == "app" {
			src_dir = d.AppDir + "/" + d.App
			basename = d.App
		}
		dst := d.BuildDir + "/" + basename

		dbg.Lvl3("Building", p, "from", src_dir, "into", basename)
		wg.Add(1)
		if p == "users" {
			go func(src, dest string) {
				defer wg.Done()
				// the users node has a 386 FreeBSD architecture
				// go won't compile on an absolute path so we need to
				// convert it to a relative one
				src_rel, _ := filepath.Rel(d.DeterDir, src)
				out, err := cliutils.Build("./" + src_rel, dest, "386", "freebsd")
				if err != nil {
					cliutils.KillGo()
					dbg.Lvl1(out)
					dbg.Fatal(err)
				}
			}(src_dir, dst)
			continue
		}
		go func(src, dest string) {
			defer wg.Done()
			// deter has an amd64, linux architecture
			src_rel, _ := filepath.Rel(d.DeterDir, src)
			dbg.Lvl3("Relative-path is", src, src_rel, d.DeterDir)
			out, err := cliutils.Build("./" + src_rel, dest, "amd64", "linux")
			if err != nil {
				cliutils.KillGo()
				dbg.Lvl1(out)
				dbg.Fatal(err)
			}
		}(src_dir, dst)
	}
	// wait for the build to finish
	wg.Wait()
	dbg.Lvl1("Build is finished after", time.Since(start))
	return nil
}

func (d *Deterlab) Deploy(rc RunConfig) error {
	dbg.Lvl1("Assembling all files and configuration options")
	os.RemoveAll(d.DeployDir)
	os.Mkdir(d.DeployDir, 0777)

	dbg.Lvl1("Writing config-files")

	// Initialize the deter-struct with our current structure (for debug-levels
	// and such), then read in the app-configuration to overwrite eventual
	// 'Machines', 'Hpn', 'Loggers' or other fields
	deter := *d
	appConfig := d.DeployDir + "/app.toml"
	deterConfig := d.DeployDir + "/deter.toml"
	ioutil.WriteFile(appConfig, []byte(rc), 0666)
	deter.ReadConfig(appConfig)

	deter.createHosts()
	d.MasterLogger = deter.MasterLogger
	app.WriteTomlConfig(deter, deterConfig)

	// Prepare special configuration preparation for each application - the
	// reading in twice of the configuration file, once for the deterConfig,
	// then for the appConfig, sets the deterConfig as defaults and overwrites
	// everything else with the actual appConfig (which comes from the
	// runconfig-file)
	switch d.App {
	case "sign", "stamp":
		conf := app.ConfigColl{}
		app.ReadTomlConfig(&conf, deterConfig)
		app.ReadTomlConfig(&conf, appConfig)
		// Calculates a tree that is used for the timestampers
		var depth int
		conf.Tree, conf.Hosts, depth, _ = graphs.TreeFromList(deter.Virt[deter.Loggers:], conf.Hpn, conf.Bf)
		dbg.Lvl2("Depth:", depth)
		dbg.Lvl2("Total hosts:", len(conf.Hosts))
		total := deter.Machines * conf.Hpn
		if len(conf.Hosts) != total {
			dbg.Fatal("Only calculated", len(conf.Hosts), "out of", total, "hosts - try changing number of",
				"machines or hosts per node")
		}
		deter.Hostnames = conf.Hosts
		// re-write the new configuration-file
		app.WriteTomlConfig(conf, appConfig)
	case "shamir":
		conf := app.ConfigShamir{}
		app.ReadTomlConfig(&conf, deterConfig)
		app.ReadTomlConfig(&conf, appConfig)
		_, conf.Hosts, _, _ = graphs.TreeFromList(deter.Virt[deter.Loggers:], conf.Hpn, conf.Hpn)
		deter.Hostnames = conf.Hosts
		// re-write the new configuration-file
		app.WriteTomlConfig(conf, appConfig)
	case "randhound":
	}
	app.WriteTomlConfig(deter, "deter.toml", d.DeployDir)
	/*
	dbg.Printf("%+v", deter)
	debug := reflect.ValueOf(deter).Elem().FieldByName("Debug")
	if debug.IsValid() {
		dbg.DebugVisible = debug.Interface().(int)
	}
	*/

	// copy the webfile-directory of the logserver to the remote directory
	err := exec.Command("cp", "-a", d.DeterDir + "/logserver/webfiles",
		d.DeterDir + "/cothority.conf", d.DeployDir).Run()
	if err != nil {
		dbg.Fatal("error copying webfiles:", err)
	}
	build, err := ioutil.ReadDir(d.BuildDir)
	for _, file := range build {
		err = exec.Command("cp", d.BuildDir + "/" + file.Name(), d.DeployDir).Run()
		if err != nil {
			dbg.Fatal("error copying build-file:", err)
		}
	}

	dbg.Lvl1("Copying over to", d.Login, "@", d.Host)
	// Copy everything over to deterlabs
	err = cliutils.Rsync(d.Login, d.Host, d.DeployDir + "/", "remote/")
	if err != nil {
		dbg.Fatal(err)
	}

	dbg.Lvl1("Done copying")

	return nil
}

func (d *Deterlab) Start() error {
	// setup port forwarding for viewing log server
	d.started = true
	dbg.Lvl3("setting up port forwarding for master logger: ", d.MasterLogger, d.Login, d.Host)
	cmd := exec.Command(
		"ssh",
		"-t",
		"-t",
		fmt.Sprintf("%s@%s", d.Login, d.Host),
		"-L",
		"8081:" + d.MasterLogger + ":10000")
	err := cmd.Start()
	if err != nil {
		dbg.Fatal("failed to setup portforwarding for logging server")
	}

	go func() {
		err := cliutils.SshRunStdout(d.Login, d.Host, "cd remote; GOMAXPROCS=8 ./users")
		if err != nil{
			dbg.Lvl3(err)
		}
		d.sshDeter <- "finished"
	}()

	return nil
}

func (d *Deterlab) Stop() error {
	killssh := exec.Command("pkill", "-f", "ssh -t -t")
	killssh.Stdout = os.Stdout
	killssh.Stderr = os.Stderr
	err := killssh.Run()
	if err != nil {
		dbg.Lvl3("Stopping ssh:", err)
	}

	if d.started {
		dbg.Lvl3("Simulation is started")
		select {
		case msg := <-d.sshDeter:
			if msg == "finished" {
				dbg.Lvl3("Received finished-message, not killing users")
				return nil
			} else {
				dbg.Lvl1("Received out-of-line message", msg)
			}
		case <-time.After(time.Second):
			dbg.Lvl3("No message waiting")
		}
	}

	dbg.Lvl3("Going to kill everything")
	go func() {
		err := cliutils.SshRunStdout(d.Login, d.Host, "cd remote; ./users -kill")
		if err != nil{
			dbg.Lvl3(err)
		}
		d.sshDeter <- "stopped"
	}()

	for {
		select {
		case msg := <-d.sshDeter:
			if msg == "stopped" {
				dbg.Lvl3("Users stopped")
				return nil
			} else {
				dbg.Lvl2("Received other command", msg, "probably the app didn't quit correctly")
			}
		case <-time.After(time.Second * 20):
			dbg.Lvl3("Timeout error when waiting for end of ssh")
			return nil
		}
	}

	return nil
}

// Reads in the deterlab-config and drops out if there is an error
func (d *Deterlab) ReadConfig(name ...string) {
	configName := "deter.toml"
	if len(name) > 0 {
		configName = name[0]
	}
	err := app.ReadTomlConfig(d, configName)
	_, caller, line, _ := runtime.Caller(1)
	who := caller + ":" + strconv.Itoa(line)
	if err != nil {
		dbg.Fatal("Couldn't read config in", who, ":", err)
	}
	dbg.DebugVisible = d.Debug
}

/*
* Write the hosts.txt file automatically
* from project name and number of servers
 */
func (d *Deterlab) createHosts() error {
	num_servers := d.Machines + d.Loggers
	nmachs, nloggers := d.Machines, d.Loggers

	// write the name of the server + \t + IP address
	ip := "10.255.0."
	name := d.Project + ".isi.deterlab.net"
	d.Phys = make([]string, 0, num_servers)
	d.Virt = make([]string, 0, num_servers)
	for i := 1; i <= num_servers; i++ {
		d.Phys = append(d.Phys, fmt.Sprintf("server-%d.%s.%s", i - 1, d.Experiment, name))
		d.Virt = append(d.Virt, fmt.Sprintf("%s%d", ip, i))
	}

	// only take the machines we need
	d.Phys = d.Phys[:nmachs + nloggers]
	d.Virt = d.Virt[:nmachs + nloggers]
	d.MasterLogger = d.Phys[0]

	return nil
}


// Checks whether host, login and project are defined. If any of them are missing, it will
// ask on the command-line.
// For the login-variable, it will try to set up a connection to d.Host and copy over the
// public key for a more easy communication
func (d *Deterlab) LoadAndCheckDeterlabVars() {
	deter := Deterlab{}
	err := app.ReadTomlConfig(&deter, "deter.toml", d.DeterDir)
	d.Host, d.Login, d.Project, d.Experiment, d.Loggers =
	deter.Host, deter.Login, deter.Project, deter.Experiment, deter.Loggers

	if err != nil {
		dbg.Lvl1("Couldn't read config-file - asking for default values")
	}

	if d.Host == "" {
		d.Host = readString("Please enter the hostname of deterlab", "users.deterlab.net")
	}

	if d.Login == "" {
		d.Login = readString("Please enter the login-name on " + d.Host, "")
	}

	if d.Project == "" {
		d.Project = readString("Please enter the project on deterlab", "SAFER")
	}

	if d.Experiment == "" {
		d.Experiment = readString("Please enter the Experiment on " + d.Project, "Dissent-CS")
	}

	if d.Loggers == 0 {
		d.Loggers = 3
	}

	app.WriteTomlConfig(*d, "deter.toml", d.DeterDir)
}

// Shows a messages and reads in a string, eventually returning a default (dft) string
func readString(msg, dft string) string {
	fmt.Printf("%s [%s]: ", msg, dft)

	reader := bufio.NewReader(os.Stdin)
	strnl, _ := reader.ReadString('\n')
	str := strings.TrimSpace(strnl)
	if str == "" {
		return dft
	}
	return str
}
