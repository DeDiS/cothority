/*
Cothority-SDA is a framework that allows testing, simulating and
deploying crypto-related protocols.

# Running a simulation

Your best starting point is the simul/-directory where you can start different
protocols and test them on your local computer. After building, you can
start any protocol you like:
	cd simul
	go build
	./simul runfiles/test_cosi.toml
Once a simulation is done, you can look at the results in the test_data-directory:
	cat test_data/test_cosi.csv
To have a simple plot of the round-time, you need to have matplotlib installed
in version 1.5.1.
	matplotlib/plot.py test_data/test_cosi.csv
If plot.py complains about missing matplotlib-library, you can install it using
	sudo easy_install "matplotlib == 1.5.1"
at least on a Mac.

# Writing your own protocol

If you want to experiment with a protocol of your own, have a look at
the protocols-package-documentation.

# Deploying

Unfortunately it's not possible to deploy the Cothority as a stand-alon app.
Well, it is possible, but there is no way to start a protocol.
*/
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dedis/cothority/lib/config"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	// Empty imports to have the init-functions called which should
	// register the protocol

	_ "github.com/dedis/cothority/protocols"
	_ "github.com/dedis/cothority/services"
)

/*
Cothority is a general node that can be used for all available protocols.
*/

// ConfigFile represents the configuration for a standalone run
var ConfigFile string
var debugVisible int

// Initialize before 'init' so we can directly use the fields as parameters
// to 'Flag'
func init() {
	flag.StringVar(&ConfigFile, "config", "cothorityd.toml", "which config-file to use")
	flag.IntVar(&debugVisible, "debug", 1, "verbosity: 0-5")
}

// Main starts the host and will setup the protocol.
func main() {
	flag.Parse()
	dbg.SetDebugVisible(debugVisible)

	if _, err := os.Stat(ConfigFile); os.IsNotExist(err) {
		// the config file does not exists, let's create it
		config, fname, err := config.CreateCothoritydConfig(ConfigFile)
		if err != nil {
			dbg.Fatal("Could not create config file:", err)
		}
		dbg.Print("fname = ", fname)
		if fname == "" {
			fname = ConfigFile
		}
		// write it down
		dbg.Lvl1("Writing the config file down in '", fname, "'")
		if err := config.Save(fname); err != nil {
			dbg.Fatal("Could not save the config file", err)
		}
		ConfigFile = fname
	}

	// Let's read the config
	conf, host, err := config.ParseCothorityd(ConfigFile)
	if err != nil {
		dbg.Fatal("Couldn't parse config:", err)
	}

	fmt.Print("\n\n\t\t\033[1mServer config to contact this cothorityd\033[0m\n\n")
	serverToml := config.NewServerToml(network.Suite, host.Entity.Public,
		conf.Addresses...)
	groupToml := config.NewGroupToml(serverToml)
	fmt.Println(groupToml.String())
	fmt.Println("\n")
	host.ListenAndBind()
	host.StartProcessMessages()
	host.WaitForClose()
}
