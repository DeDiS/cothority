package service

import (
	"github.com/dedis/kyber"
	"github.com/dedis/onet"
)

// PROTOSTART

// CheckConfig asks whether the pop-config and the attendees are available.
type CheckConfig struct {
	PopHash   []byte
	Attendees []kyber.Point
}

// CheckConfigReply sends back an integer for the Pop. 0 means no config yet,
// other values are defined as constants.
// If PopStatus == PopStatusOK, then the Attendees will be the common attendees between
// the two nodes.
type CheckConfigReply struct {
	PopStatus int
	PopHash   []byte
	Attendees []kyber.Point
}

// MergeConfig asks if party is ready to merge
type MergeConfig struct {
	// FinalStatement of current party
	Final *FinalStatement
	// Hash of PopDesc party to merge with
	ID []byte
}

// MergeConfigReply responds with info of asked party
type MergeConfigReply struct {
	// status of merging process
	PopStatus int
	// hash of party was asking to merge
	PopHash []byte
	// FinalStatement of party was asked to merge
	Final *FinalStatement
}


// PinRequest will print a random pin on stdout if the pin is empty. If
// the pin is given and is equal to the random pin chosen before, the
// public-key is stored as a reference to the allowed client.
type PinRequest struct {
	Pin    string
	Public kyber.Point
}

// StoreConfig presents a config to store
type StoreConfig struct {
	Desc      *PopDesc
	Signature []byte
}

// StoreConfigReply gives back the hash.
// TODO: StoreConfigReply will give in a later version a handler that can be used to
// identify that config.
type StoreConfigReply struct {
	ID []byte
}


// FinalizeRequest asks to finalize on the given descid-popconfig.
type FinalizeRequest struct {
	DescID    []byte
	Attendees []kyber.Point
	Signature []byte
}


// FinalizeResponse returns the FinalStatement if all conodes already received
// a PopDesc and signed off. The FinalStatement holds the updated PopDesc, the
// pruned attendees-public-key-list and the collective signature.
type FinalizeResponse struct {
	Final *FinalStatement
}

// FetchRequest asks to get FinalStatement
type FetchRequest struct {
	ID               []byte
	ReturnUncomplete *bool
}

// MergeRequest asks to start merging process for given Party
type MergeRequest struct {
	ID        []byte
	Signature []byte
}

// GetProposals asks the conode to return a list of all waiting proposals. A waiting
// proposal is either deleted after 1h or if it has been confirmed using
// StoreConfig.
type GetProposals struct {
}

// GetProposalsReply returns the list of all waiting proposals on that node.
type GetProposalsReply struct {
	Proposals []PopDesc
}


// VerifyLink returns if a given public key is linked.
type VerifyLink struct {
	Public kyber.Point
}

// VerifyLinkReply returns true if the public key is in the admin-list.
type VerifyLinkReply struct {
	Exists bool
}

// FinalStatement is the final configuration holding all data necessary
// for a verifier.
type FinalStatement struct {
	// Desc is the description of the pop-party.
	Desc *PopDesc
	// Attendees holds a slice of all public keys of the attendees.
	Attendees []kyber.Point
	// Signature is created by all conodes responsible for that pop-party
	Signature []byte
	// Flag indicates, that party was merged
	Merged bool
}

// The toml-structure for (un)marshaling with toml
type finalStatementToml struct {
	Desc      *popDescToml
	Attendees []string
	Signature string
	Merged    bool
}

// PopDesc holds the name, date and a roster of all involved conodes.
type PopDesc struct {
	// Name and purpose of the party.
	Name string
	// DateTime of the party. It is in the following format, following UTC:
	//   YYYY-MM-DD HH:mm
	DateTime string
	// Location of the party
	Location string
	// Roster of all responsible conodes for that party.
	Roster *onet.Roster
	// List of parties to be merged
	Parties []*ShortDesc
}

// represents a PopDesc in string-version for toml.
type popDescToml struct {
	Name     string
	DateTime string
	Location string
	Roster   [][]string
	Parties  []shortDescToml
}

// ShortDesc represents Short Description of Pop party
// Used in merge configuration
type ShortDesc struct {
	Location string
	Roster   *onet.Roster
}

type shortDescToml struct {
	Location string
	Roster   [][]string
}

// PopToken represents pop-token
type PopToken struct {
	Final   *FinalStatement
	Private kyber.Scalar
	Public  kyber.Point
}

type popTokenToml struct {
	Final   *finalStatementToml
	Private string
	Public  string
}
