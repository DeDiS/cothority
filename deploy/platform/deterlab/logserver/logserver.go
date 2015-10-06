package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	dbg "github.com/dedis/cothority/lib/debug_lvl"
	"github.com/dedis/cothority/lib/graphs"

	"golang.org/x/net/websocket"
	"github.com/dedis/cothority/deploy/platform"
	"github.com/dedis/cothority/lib/app"
)

var deter platform.Deterlab
var conf app.ConfigColl
var addr, master string
var homePage *template.Template

type Home struct {
	LogServer        string
	Hosts            string
	Depth            string
	BranchingFactor  string
	HostsPerNode     string
	NumberOfMessages string
	Rate             string
}

var Log Logger

func init() {
	flag.StringVar(&addr, "addr", "", "the address of the logging server")
	flag.StringVar(&master, "master", "", "address of the master of this node")

	Log = Logger{
		Slock: sync.RWMutex{},
		Sox:   make(map[*websocket.Conn]bool),
		Mlock: sync.RWMutex{},
		Msgs:  make([][]byte, 0, 100000),
	}
	rand.Seed(42)
}

func main() {
	deter.ReadConfig()
	app.ReadTomlConfig(&conf, "app.toml")

	runtime.GOMAXPROCS(runtime.NumCPU())
	// read in from flags the port I should be listening on
	flag.Parse()

	if master == "" {
		isMaster = true
	}
	var role string
	if isMaster {
		role = "Master"
	} else {
		role = "Servent"
	}
	dbg.Lvl3("running logserver", role, "with nmsgs", conf.Nmsgs, "branching factor: ", conf.Bf)
	if isMaster {
		var err error
		homePage, err = template.ParseFiles("webfiles/home.html")
		if err != nil {
			log.Fatal("unable to parse home.html", err)
		}

		debugServers := getDebugServers()
		for _, s := range debugServers {
			reverseProxy(s)
		}

		dbg.Lvl4("Log server", role, "running at :", addr)
		// /webfiles/Chart.js/Chart.min.js
		http.HandleFunc("/", homeHandler)
		fs := http.FileServer(http.Dir("webfiles/"))
		http.Handle("/webfiles/", http.StripPrefix("/webfiles/", fs))
	} else {
		retry:
		tries := 0
		var err error
		origin := "http://localhost/"
		url := "ws://" + master + "/_log"
		wsmaster, err = websocket.Dial(url, "", origin)
		if err != nil {
			tries += 1
			time.Sleep(time.Second)
			dbg.Lvl4("Slave log server could not connect to logger master (", master, ") .. Trying again (", tries, ")")
			goto retry
		}
		dbg.Lvl4("Slave Log server", role, "running at :", addr, "& connected to Master ")
	}
	http.Handle("/_log", websocket.Handler(logEntryHandler))
	http.Handle("/log", websocket.Handler(logHandler))
	http.HandleFunc("/htmllog", logHandlerHtml)
	http.HandleFunc("/htmllogrev", logHandlerHtmlReverse)
	dbg.Lvl1("Log-server", addr, "ready for service")
	log.Fatalln("ERROR: ", http.ListenAndServe(addr, nil))
	// now combine that port
}

type Logger struct {
	Slock sync.RWMutex
	Sox   map[*websocket.Conn]bool

	Mlock sync.RWMutex
	Msgs  [][]byte
	End   int
}

// keep a list of websockets that people are listening on

// keep a log of messages received

func logEntryHandler(ws *websocket.Conn) {
	var data []byte
	err := websocket.Message.Receive(ws, &data)
	for err == nil {
		//dbg.Lvl4("logEntryHandler", isMaster)
		if !isMaster {
			websocket.Message.Send(wsmaster, data)
		} else {
			Log.Mlock.Lock()
			Log.Msgs = append(Log.Msgs, data)
			Log.End += 1
			Log.Mlock.Unlock()
		}
		err = websocket.Message.Receive(ws, &data)
	}
	dbg.Lvl4("log server client error:", err)
}

func logHandler(ws *websocket.Conn) {
	dbg.Lvl4(master, "log server serving /log (websocket)")
	i := 0
	for {
		Log.Mlock.RLock()
		end := Log.End
		Log.Mlock.RUnlock()
		if i >= end {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		Log.Mlock.RLock()
		msg := Log.Msgs[i]
		Log.Mlock.RUnlock()
		_, err := ws.Write(msg)
		if err != nil {
			dbg.Lvl4("unable to write to log websocket")
			return
		}

		i++
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		dbg.Lvl4("home handler is handling non-home request")
		http.NotFound(w, r)
		return
	}
	dbg.Lvl4(master, "log server serving ", r.URL)
	host := r.Host
	// fmt.Println(host)
	ws := "ws://" + host + "/log"

	err := homePage.Execute(w, Home{ws, strconv.Itoa(deter.Machines * conf.Hpn), strconv.Itoa(conf.Hpn), strconv.Itoa(conf.Bf),
		strconv.Itoa(conf.Hpn), strconv.Itoa(conf.Nmsgs), strconv.Itoa(conf.Rate)})
	if err != nil {
		panic(err)
		log.Fatal(err)
	}
}

func logHandlerHtml(w http.ResponseWriter, r *http.Request) {
	dbg.Lvl4("Log handler: ", r.URL, "-", len(Log.Msgs))
	//host := r.Host
	// fmt.Println(host)
	for i, _ := range Log.Msgs {
		var jsonlog map[string]*json.RawMessage
		err := json.Unmarshal(Log.Msgs[i], &jsonlog)
		if err != nil {
			dbg.Lvl1("Couldn't unmarshal string")
		}
		w.Write([]byte(fmt.Sprintf("%s - %s - %s - %s", *jsonlog["etime"], *jsonlog["eapp"],
			*jsonlog["ehost"], *jsonlog["emsg"])))
		w.Write([]byte("\n"))
	}
}

func logHandlerHtmlReverse(w http.ResponseWriter, r *http.Request) {
	dbg.Lvl4("Log handler: ", r.URL, "-", len(Log.Msgs))
	//host := r.Host
	// fmt.Println(host)
	for i := len(Log.Msgs) - 1; i >= 0; i-- {
		var jsonlog map[string]*json.RawMessage
		err := json.Unmarshal(Log.Msgs[i], &jsonlog)
		if err != nil {
			dbg.Lvl1("Couldn't unmarshal string")
		}

		w.Write([]byte(fmt.Sprintf("%s - %s - %s - %s", *jsonlog["etime"], *jsonlog["eapp"],
			*jsonlog["ehost"], *jsonlog["emsg"])))
		w.Write([]byte("\n"))
	}
}

func NewReverseProxy(target *url.URL) *httputil.ReverseProxy {
	director := func(r *http.Request) {
		r.URL.Scheme = target.Scheme
		r.URL.Host = target.Host

		// get rid of the (/d/short_name)/debug of the url path requested
		//  --> long_name/debug
		pathComp := strings.Split(r.URL.Path, "/")
		// remove the first two components /d/short_name
		pathComp = pathComp[3:]
		r.URL.Path = target.Path + "/" + strings.Join(pathComp, "/")
		dbg.Lvl4("redirected to: ", r.URL.String())
	}
	dbg.Lvl4("setup reverse proxy for destination url:", target.Host, target.Path)
	return &httputil.ReverseProxy{Director: director}
}

func proxyDebugHandler(p *httputil.ReverseProxy) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		dbg.Lvl4("proxy serving request for: ", r.URL)
		p.ServeHTTP(w, r)
	}
}

var timesSeen = make(map[string]int)

func reverseProxy(server string) {
	remote, err := url.Parse("http://" + server)
	if err != nil {
		panic(err)
	}
	// get the short name of this remote
	s := strings.Split(server, ".")[0]
	short := s + "-" + strconv.Itoa(timesSeen[s])
	timesSeen[s] = timesSeen[s] + 1

	// setup a reverse proxy s.t.
	//
	// "/d/short_name/debug" -> http://server/debug
	//
	proxy := NewReverseProxy(remote)

	//dbg.Lvl4("setup proxy for: /d/"+short+"/", " it points to : "+server)
	// register the reverse proxy forwarding for this server
	http.HandleFunc("/d/" + short + "/", proxyDebugHandler(proxy))
}

func getDebugServers() []string {
	dbg.Lvl3("Creating list of debug-servers")

	// create mapping from virtual nodes to physical nodes
	vpmap := make(map[string]string)
	for i := range deter.Phys {
		vpmap[deter.Virt[i]] = deter.Phys[i]
	}

	debugServers := make([]string, 0, len(deter.Virt))
	if conf.Tree != nil {
		conf.Tree.TraverseTree(func(t *graphs.Tree) {
			h, p, err := net.SplitHostPort(t.Name)
			if err != nil {
				log.Fatal("improperly formatted hostport:", err)
			}
			pn, _ := strconv.Atoi(p)
			s := net.JoinHostPort(vpmap[h], strconv.Itoa(pn + 2))
			debugServers = append(debugServers, s)
		})
	}
	return debugServers
}

var isMaster bool
var wsmaster *websocket.Conn

