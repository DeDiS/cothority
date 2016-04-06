package sda

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	"github.com/satori/go.uuid"
	"os"
	"path"
)

// ProtocolInstance is the interface that instances have to use in order to be
// recognized as protocols
type ProtocolInstance interface {
	// Start is called when a leader has created its tree configuration and
	// wants to start a protocol, it calls host.StartProtocol(protocolID), that
	// in turns instantiate a new protocol (with a fresh token), and then call
	// Start on it.
	Start() error
	// Dispatch is called as a go-routine and can be used to handle channels
	Dispatch() error
	// Shutdown cleans up the resources used by this protocol instance
	Shutdown() error
}

// ProtocolID is the type representing a Protocol
type ProtocolID uuid.UUID

// NilProtocolID represents an empty ProtocolID
var NilProtocolID = ProtocolID(uuid.Nil)

func (p *ProtocolID) String() string {
	return uuid.UUID(*p).String()
}

// NewProtocol is a convenience to represent the cosntructor function of a
// ProtocolInstance
type NewProtocol func(*Node) (ProtocolInstance, error)

// Service is a generic interface to define any type of services.
type Service interface {
	NewProtocol(n *Node) (ProtocolInstance, error)
	// ProcessRequest is the function that will be called when a external client
	// using the CLI will contact this service with a request packet.
	// Each request has a field ServiceID, so each time the Host (dispatcher)
	// receives a request, it looks whether it knows the Service it is for and
	// then dispatch it through ProcessRequest.
	ProcessRequest(*network.Entity, *Request)
}

type protocolFactory interface {
	Instantiate(n *Node) (ProtocolInstance, error)
}

// ProtocolFactory is the global var that can instantiate any ProtocolInstance
var ProtocolFactory = globalProtocolFactory{
	// services that will be called to instantiate a node
	services: make(map[ServiceID]NewProtocol),
	// static functions that will be called to instantiate a node
	statics:      make(map[ProtocolID]NewProtocol),
	translations: make(map[string]ProtocolID),
	reverse:      make(map[ProtocolID]string),
}

type globalProtocolFactory struct {
	// services that will be called to instantiate a node
	services map[ServiceID]NewProtocol
	// static functions that will be called to instantiate a node
	statics      map[ProtocolID]NewProtocol
	translations map[string]ProtocolID
	reverse      map[ProtocolID]string
}

func (gpf *globalProtocolFactory) Instantiate(n *Node) (ProtocolInstance, error) {
	var t = n.Token()
	var np NewProtocol
	var ok bool
	// the protocols statics functions
	if t.ServiceID == NilServiceID {
		if np, ok = gpf.statics[n.Token().ProtoID]; !ok {
			return nil, errors.New("No static functions can instantiate that")
		}
		// the services functions
	} else if np, ok = gpf.services[n.Token().ServiceID]; !ok {
		return nil, errors.New("No services can instantiate that protocol")
	}
	return np(n)
}

func (gpf *globalProtocolFactory) RegisterNewProtocol(name string, np NewProtocol) {
	id := ProtocolID(uuid.NewV5(uuid.NamespaceURL, name))
	gpf.statics[id] = np
	gpf.translations[name] = id
	gpf.reverse[id] = name
}

// RegisterNewProtocol takes the name of the protocol and registers its static
// function here.
func RegisterNewProtocol(name string, np NewProtocol) {
	ProtocolFactory.RegisterNewProtocol(name, np)
}

func (gpf *globalProtocolFactory) registerService(servID ServiceID, np NewProtocol) {
	gpf.services[servID] = np
}

func (gpf *globalProtocolFactory) ProtocolID(name string) ProtocolID {
	return gpf.translations[name]
}

func (gpf *globalProtocolFactory) Name(protoID ProtocolID) string {
	return gpf.reverse[protoID]
}

// ServiceID is the type representing the ID of a Service running
type ServiceID uuid.UUID

// String returns the string version of this ID
func (s *ServiceID) String() string {
	return uuid.UUID(*s).String()
}

// Equal returns true if both IDs are equal
func (s *ServiceID) Equal(s2 ServiceID) bool {
	return uuid.Equal(uuid.UUID(*s), uuid.UUID(s2))
}

// NilServiceID is the empty ID
var NilServiceID = ServiceID(uuid.Nil)

// NewServiceFunc is the type of a function that is used to instantiate a given Service
// A service is initialized with a Host (to send messages to someone), the
// overlay (to register a Tree + EntityList + start new node), and a path where
// it can finds / write everything it needs
type NewServiceFunc func(h *Host, o *Overlay, path string) Service

// A serviceFactory is used to register a NewServiceFunc
type serviceFactory struct {
	cons map[ServiceID]NewServiceFunc
	// translations between name of a Service and its ServiceID. Used to register a
	// Service using a name.
	translations map[string]ServiceID
	// Inverse mapping of ServiceId => string
	inverseTr map[ServiceID]string
}

// ServiceFactory is the global service factory to instantiate Services
var ServiceFactory = serviceFactory{
	cons:         make(map[ServiceID]NewServiceFunc),
	translations: make(map[string]ServiceID),
	inverseTr:    make(map[ServiceID]string),
}

// RegisterByName takes an name, creates a ServiceID out of it and store the
// mapping and the creation function.
func (s *serviceFactory) Register(name string, fn NewServiceFunc) {
	id := ServiceID(uuid.NewV5(uuid.NamespaceURL, name))
	if _, ok := s.cons[id]; ok {
		// called at init time so better panic than to continue
		dbg.Lvl1("RegisterService():", name)
	}
	s.cons[id] = fn
	s.translations[name] = id
	s.inverseTr[id] = name
}

// RegisterNewService is a wrapper around service factory
func RegisterNewService(name string, fn NewServiceFunc) {
	ServiceFactory.Register(name, fn)
}

// RegisteredServices returns all the services registered
func (s *serviceFactory) registeredServicesID() []ServiceID {
	var ids = make([]ServiceID, 0, len(s.cons))
	for id := range s.cons {
		ids = append(ids, id)
	}
	return ids
}

// RegisteredServicesByName returns all the names of the services registered
func (s *serviceFactory) RegisteredServicesName() []string {
	var names = make([]string, 0, len(s.translations))
	for n := range s.translations {
		names = append(names, n)
	}
	return names
}

// ServiceID returns the ServiceID out of the name of the service
func (s *serviceFactory) ServiceID(name string) ServiceID {
	var id ServiceID
	var ok bool
	if id, ok = s.translations[name]; !ok {
		return NilServiceID
	}
	return id
}

// Name returns the Name out of the ID
func (s *serviceFactory) Name(id ServiceID) string {
	var name string
	var ok bool
	if name, ok = s.inverseTr[id]; !ok {
		return ""
	}
	return name
}

// start launches a new service
func (s *serviceFactory) start(name string, host *Host, o *Overlay, path string) (Service, error) {
	var id ServiceID
	var ok bool
	if id, ok = s.translations[name]; !ok {
		return nil, errors.New("No Service for this name: " + name)
	}
	var fn NewServiceFunc
	if fn, ok = s.cons[id]; !ok {
		return nil, errors.New("No Service for this id:" + fmt.Sprintf("%v", id))
	}
	return fn(host, o, path), nil
}

// serviceStore is the place where all instantiated services are stored
// It gives access to :  all the currently running services and is handling the
// configuration path for them
type serviceStore struct {
	// the actual services
	services map[ServiceID]Service
	// the config paths
	paths map[ServiceID]string
}

const configFolder = "config"

// newServiceStore will create a serviceStore out of all the registered Service
// it creates the path for the config folder of each service. basically
// ```configFolder / *nameOfService*```
func newServiceStore(h *Host, o *Overlay) *serviceStore {
	// check if we have a config folder
	if err := os.MkdirAll(configFolder, 0777); err != nil {
		_, ok := err.(*os.PathError)
		if !ok {
			// we cannot continue from here
			panic(err)
		}
	}
	services := make(map[ServiceID]Service)
	configs := make(map[ServiceID]string)
	ids := ServiceFactory.registeredServicesID()
	for _, id := range ids {
		name := ServiceFactory.Name(id)
		pwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		configName := path.Join(pwd, configFolder, name)
		if err := os.MkdirAll(configName, 0666); err != nil {
			dbg.Error("Service", name, "Might not work properly: error setting up its config directory(", configName, "):", err)
		}
		s, err := ServiceFactory.start(name, h, o, configName)
		if err != nil {
			dbg.Error("Trying to instantiate service:", err)
		}
		dbg.Lvl2("Started Service", name, " (config in", configName, ")")
		services[id] = s
		configs[id] = configName
		// !! register to the ProtocolFactory !!
		ProtocolFactory.registerService(id, s.NewProtocol)
	}
	return &serviceStore{services, configs}
}

// TODO
func (s *serviceStore) AvailableServices() []string {
	panic("not implemented")
}

// TODO
func (s *serviceStore) Service(name string) Service {
	return s.serviceByString(name)
}

// TODO
func (s *serviceStore) serviceByString(name string) Service {
	panic("Not implemented")
}

func (s *serviceStore) serviceByID(id ServiceID) Service {
	var serv Service
	var ok bool
	if serv, ok = s.services[id]; !ok {
		return nil
	}
	return serv
}

// A Request is a generic packet to represent any kind of request a Service is
// ready to process. It is simply a JSON packet containing two fields:
// * Service: a string representing the name of the service for whom the packet
// is intended for.
// * Data: contains all the information of the request
type Request struct {
	// Name of the service to direct this request to
	Service ServiceID `json:"service_id"`
	// Type is the type of the underlying request
	Type string `json:"type"`
	// Data containing all the information in the request
	Data *json.RawMessage `json:"data"`
}

// RequestType is the type that registered by the network library
var RequestType = network.RegisterMessageType(Request{})
