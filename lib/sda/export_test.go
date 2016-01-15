package sda

import (
	"github.com/dedis/cothority/lib/network"
)

// Export some private functions of Host for testing

func (n *Host) AddPendingTreeMarshal(tm *TreeMarshal) {
	n.addPendingTreeMarshal(tm)
}

func (n *Host) CheckPendingTreeMarshal(el *EntityList) {
	n.checkPendingTreeMarshal(el)
}

func (n *Host) SendSDAData(id *network.Entity, msg *SDAData) error {
	return n.sendSDAData(id, msg)
}

func (n *Host) Receive() network.NetworkMessage {
	return n.receive()
}

func (n *Host) ProtocolInstantiate(tok *Token, tn *TreeNode) (ProtocolInstance, error) {
	return n.protocolInstantiate(tok, tn)
}
