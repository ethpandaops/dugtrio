package rpc

import "strconv"

type NodeIdentity struct {
	PeerID             string   `json:"peer_id"`
	Enr                string   `json:"enr"`
	P2PAddresses       []string `json:"p2p_addresses"`
	DiscoveryAddresses []string `json:"discovery_addresses"`
	Metadata           struct {
		Attnets           string      `json:"attnets"`
		Syncnets          string      `json:"syncnets"`
		SeqNumber         interface{} `json:"seq_number"`                    // Can be string or int depending on client
		CustodyGroupCount interface{} `json:"custody_group_count,omitempty"` // MetadataV3 field for Fulu
	} `json:"metadata"`
}

func (nodeIdentity *NodeIdentity) GetCustodyGroupCount() uint16 {
	switch nodeIdentity.Metadata.CustodyGroupCount.(type) {
	case int:
		return uint16(nodeIdentity.Metadata.CustodyGroupCount.(int))
	case string:
		count, err := strconv.ParseUint(nodeIdentity.Metadata.CustodyGroupCount.(string), 10, 16)
		if err != nil {
			return 0
		}
		return uint16(count)
	default:
		return 0
	}
}
