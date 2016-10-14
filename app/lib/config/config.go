package config

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"os/user"

	"github.com/BurntSushi/toml"
	"github.com/dedis/cothority/crypto"
	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/network"
	"github.com/dedis/cothority/sda"
	"github.com/dedis/crypto/abstract"
	"github.com/dedis/crypto/config"
)

var in *bufio.Reader
var out io.Writer

func init() {
	in = bufio.NewReader(os.Stdin)
	out = os.Stdout
}

// CothoritydConfig is the configuration structure of the cothority daemon.
type CothoritydConfig struct {
	Public  string
	Private string
	Address network.Address
	TLSCert network.TLSCertPEM
	TLSKey  network.TLSKeyPEM
}

// Save will save this CothoritydConfig to the given file name. It
// will return an error if the file couldn't be created or if
// there is an error in the encoding.
func (hc *CothoritydConfig) Save(file string) error {
	fd, err := os.Create(file)
	if err != nil {
		return err
	}
	err = toml.NewEncoder(fd).Encode(hc)
	if err != nil {
		return err
	}
	return nil
}

// ParseCothorityd parses the config file into a CothoritydConfig.
// It returns the CothoritydConfig, the Host so we can already use it, and an error if
// the file is inaccessible or has wrong values in it.
func ParseCothorityd(file string) (*CothoritydConfig, *sda.Conode, error) {
	hc := &CothoritydConfig{}
	_, err := toml.DecodeFile(file, hc)
	if err != nil {
		return nil, nil, err
	}
	// Try to decode the Hex values
	secret, err := crypto.ReadScalarHex(network.Suite, hc.Private)
	if err != nil {
		return nil, nil, err
	}
	point, err := crypto.ReadPubHex(network.Suite, hc.Public)
	if err != nil {
		return nil, nil, err
	}
	si := network.NewServerIdentityTLS(point, hc.Address, hc.TLSCert)
	router, err := network.NewTLSRouter(si, hc.TLSKey)
	if err != nil {
		return nil, nil, err
	}
	conode := sda.NewConode(router, secret)
	return hc, conode, nil
}

// CreateCothoritydConfig uses stdin to get the address. Then it creates
// a private/public key pair.
// It takes the default config file as argument, and returns the
// CothoritydConfig created, the config file name, and any error if occured.
func CreateCothoritydConfig(defaultFile string) (*CothoritydConfig, string, error) {
	reader := bufio.NewReader(os.Stdin)
	var err error
	var str string
	// IP:PORT
	fmt.Println("[+] Type the IP:PORT (ipv4) address of this host (accessible from Internet):")
	str, err = reader.ReadString('\n')
	str = strings.TrimSpace(str)
	address := network.NewTCPAddress(str)
	if !address.Valid() {
		return nil, "", fmt.Errorf("Invalid address: %s", address)
	}

	fmt.Println("[+] Creation of the private and public keys...")
	kp := config.NewKeyPair(network.Suite)
	privStr, err := crypto.ScalarHex(network.Suite, kp.Secret)
	if err != nil {
		return nil, "", fmt.Errorf("Could not parse private key. Abort.")
	}
	pubStr, err := crypto.PubHex(network.Suite, kp.Public)
	if err != nil {
		return nil, "", fmt.Errorf("Could not parse public key. Abort.")
	}
	fmt.Println("\tPrivate:\t", privStr)
	fmt.Println("\tPublic: \t", pubStr)

	fmt.Println("[+] Name of the config file [", defaultFile, "]:")
	fname, err := reader.ReadString('\n')
	fname = strings.TrimSpace(fname)

	config := &CothoritydConfig{
		Public:  pubStr,
		Private: privStr,
		Address: address,
	}
	return config, fname, err
}

// GroupToml holds the data of the group.toml file.
type GroupToml struct {
	Description string
	Servers     []*ServerToml `toml:"servers"`
}

// NewGroupToml creates a new GroupToml struct from the given ServerTomls.
// Currently used together with calling String() on the GroupToml to output
// a snippet which can be used to create a CoSi group.
func NewGroupToml(servers ...*ServerToml) *GroupToml {
	return &GroupToml{
		Servers: servers,
	}
}

// ServerToml is one entry in the group.toml file describing one server to use for
// the cothority.
type ServerToml struct {
	Address     network.Address
	Public      string
	Description string
	Cert        network.TLSCertPEM
}

// Group holds the Roster and the server-description.
type Group struct {
	Roster      *sda.Roster
	description map[*network.ServerIdentity]string
}

// GetDescription returns the description of a ServerIdentity.
func (g *Group) GetDescription(e *network.ServerIdentity) string {
	return g.description[e]
}

// ReadGroupDescToml reads a group.toml file and returns the list of ServerIdentities
// and descriptions in the file.
// If the file couldn't be decoded or doesn't hold valid ServerIdentities,
// an error is returned.
func ReadGroupDescToml(f io.Reader) (*Group, error) {
	group := &GroupToml{}
	_, err := toml.DecodeReader(f, group)
	if err != nil {
		return nil, err
	}
	// convert from ServerTomls to entities
	var sis = make([]*network.ServerIdentity, len(group.Servers))
	var descs = make(map[*network.ServerIdentity]string)
	for i, s := range group.Servers {
		si, err := s.toServerIdentity(network.Suite)
		if err != nil {
			return nil, err
		}
		sis[i] = si
		descs[si] = s.Description
	}
	el := sda.NewRoster(sis)
	return &Group{el, descs}, nil
}

// ReadGroupToml reads a group.toml file and returns the list of ServerIdentity
// described in the file.
// If the file holds an invalid ServerIdentity-description, an error is returned.
func ReadGroupToml(f io.Reader) (*sda.Roster, error) {
	group, err := ReadGroupDescToml(f)
	if err != nil {
		return nil, err
	}
	return group.Roster, nil
}

// Save writes the GroupToml definition into the file given by its name.
// It will return an error if the file couldn't be created or if writing
// to it failed.
func (gt *GroupToml) Save(fname string) error {
	file, err := os.Create(fname)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(gt.String())
	return err
}

// String returns the TOML representation of this GroupToml.
func (gt *GroupToml) String() string {
	var buff bytes.Buffer
	if gt.Description == "" {
		gt.Description = "Description of your cothority roster"
	}
	for _, s := range gt.Servers {
		if s.Description == "" {
			s.Description = "Description of your server"
		}
	}
	enc := toml.NewEncoder(&buff)
	if err := enc.Encode(gt); err != nil {
		return "Error encoding grouptoml" + err.Error()
	}
	return buff.String()
}

// toServerIdentity converts this ServerToml struct to a ServerIdentity.
func (s *ServerToml) toServerIdentity(suite abstract.Suite) (*network.ServerIdentity, error) {
	pubR := strings.NewReader(s.Public)
	public, err := crypto.ReadPub64(suite, pubR)
	if err != nil {
		return nil, err
	}
	si := network.NewServerIdentityTLS(public, s.Address, s.Cert)
	return si, nil
}

// NewServerToml takes a public key and an address and returns
// the corresponding ServerToml.
// If an error occurs, it will be printed to StdErr and nil
// is returned.
func NewServerToml(suite abstract.Suite, public abstract.Point, addr network.Address,
	cert network.TLSCertPEM) *ServerToml {
	var buff bytes.Buffer
	if err := crypto.WritePub64(suite, &buff, public); err != nil {
		log.Error("Error writing public key")
		return nil
	}
	return &ServerToml{
		Address: addr,
		Public:  buff.String(),
		Cert:    cert,
	}
}

// String returns the TOML representation of the ServerToml.
func (s *ServerToml) String() string {
	var buff bytes.Buffer
	if s.Description == "" {
		s.Description = "## Put your description here for convenience ##"
	}
	enc := toml.NewEncoder(&buff)
	if err := enc.Encode(s); err != nil {
		return "## Error encoding server informations ##" + err.Error()
	}
	return buff.String()
}

// TildeToHome takes a path and replaces an eventual "~" with the home-directory.
// If the user-directory is not defined it will return a path relative to the
// root-directory "/".
func TildeToHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		usr, err := user.Current()
		log.ErrFatal(err, "Got error while fetching home-directory")
		return usr.HomeDir + path[1:]
	}
	return path
}

// Input prints the arguments given with an 'input'-format and
// proposes the 'def' string as default. If the user presses
// 'enter', the 'dev' will be returned.
// In the case of an error it will Fatal.
func Input(def string, args ...interface{}) string {
	fmt.Fprint(out, args...)
	fmt.Fprintf(out, " [%s]: ", def)
	str, err := in.ReadString('\n')
	if err != nil {
		log.Fatal("Could not read input.")
	}
	str = strings.TrimSpace(str)
	if str == "" {
		return def
	}
	return str
}

// Inputf takes a format string and arguments and calls
// Input.
func Inputf(def string, f string, args ...interface{}) string {
	return Input(def, fmt.Sprintf(f, args...))
}

// InputYN asks a Yes/No question. Anything else than upper/lower-case
// 'y' will be interpreted as no.
func InputYN(def bool, args ...interface{}) bool {
	defStr := "Yn"
	if !def {
		defStr = "Ny"
	}
	return strings.ToLower(string(Input(defStr, args...)[0])) == "y"
}

// Copy makes a copy of a local file with the same file-mode-bits set.
func Copy(dst, src string) error {
	info, err := os.Stat(dst)
	if err == nil && info.IsDir() {
		return Copy(path.Join(dst, path.Base(src)), src)
	}
	fSrc, err := os.Open(src)
	if err != nil {
		return err
	}
	defer fSrc.Close()
	stat, err := fSrc.Stat()
	if err != nil {
		return err
	}
	fDst, err := os.OpenFile(dst, os.O_CREATE|os.O_RDWR, stat.Mode())
	if err != nil {
		return err
	}
	defer fDst.Close()
	_, err = io.Copy(fDst, fSrc)
	return err
}
