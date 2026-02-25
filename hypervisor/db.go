package hypervisor

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/projecteru2/cocoon/types"
)

// VMRecord is the persisted record for a single VM.
type VMRecord struct {
	types.VM

	StorageConfigs []*types.StorageConfig `json:"storage_configs"`
	NetworkConfigs []*types.NetworkConfig `json:"network_configs,omitempty"`
	BootConfig     *types.BootConfig      `json:"boot_config,omitempty"`    // nil for UEFI boot (cloudimg)
	ImageBlobIDs   map[string]struct{}    `json:"image_blob_ids,omitempty"` // blob hex set for GC pinning
}

// VMIndex is the top-level DB structure for a hypervisor backend.
type VMIndex struct {
	VMs   map[string]*VMRecord `json:"vms"`
	Names map[string]string    `json:"names"` // name → VM ID
}

// Init implements storage.Initer.
func (idx *VMIndex) Init() {
	if idx.VMs == nil {
		idx.VMs = make(map[string]*VMRecord)
	}
	if idx.Names == nil {
		idx.Names = make(map[string]string)
	}
}

// GenerateID returns a random 16-character hex string (8 bytes of entropy).
func GenerateID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// ResolveVMRef resolves a ref (exact ID, name, or ID prefix ≥3 chars) to a full VM ID.
func ResolveVMRef(idx *VMIndex, ref string) (string, error) {
	if idx.VMs[ref] != nil {
		return ref, nil
	}
	if id, ok := idx.Names[ref]; ok && idx.VMs[id] != nil {
		return id, nil
	}
	if len(ref) >= 3 {
		var match string
		for id := range idx.VMs {
			if strings.HasPrefix(id, ref) {
				if match != "" {
					return "", fmt.Errorf("ambiguous ref %q: multiple matches", ref)
				}
				match = id
			}
		}
		if match != "" {
			return match, nil
		}
	}
	return "", ErrNotFound
}
