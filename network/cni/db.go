package cni

import (
	"encoding/json"

	"github.com/projecteru2/cocoon/types"
)

// networkRecord is one NIC's persisted network state.
// Keyed by a generated network ID (unique per NIC, not per VM).
type networkRecord struct {
	types.Network

	// VMID links this network back to the owning VM.
	VMID string `json:"vm_id"`
	// IfName is the CNI interface name inside the netns (eth0, eth1, ...).
	IfName string `json:"if_name"`
	// CNIResult stores the raw CNI ADD result so that CNI DEL can replay it.
	CNIResult json.RawMessage `json:"cni_result,omitempty"`
}

// networkIndex is the top-level DB structure for the CNI network provider.
type networkIndex struct {
	// Networks is keyed by network ID (not VM ID).
	// A VM with 2 NICs has 2 entries here.
	Networks map[string]*networkRecord `json:"networks"`
}

// Init implements storage.Initer.
func (idx *networkIndex) Init() {
	if idx.Networks == nil {
		idx.Networks = make(map[string]*networkRecord)
	}
}

// byVMID returns all network records belonging to vmID.
func (idx *networkIndex) byVMID(vmID string) []*networkRecord {
	var out []*networkRecord
	for _, rec := range idx.Networks {
		if rec != nil && rec.VMID == vmID {
			out = append(out, rec)
		}
	}
	return out
}
