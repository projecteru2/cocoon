package hypervisor

import (
	"fmt"
	"strings"

	"github.com/projecteru2/cocoon/types"
)

// VMRecord is the persisted record for a single VM.
//
// StorageConfigs and NetworkConfigs live on the embedded types.VM so that
// a value-copy (info := rec.VM) automatically includes them — no manual
// field copying needed.  The JSON tags are on types.VM; do NOT duplicate
// them here or Go's encoding/json will silently shadow the promoted fields.
type VMRecord struct {
	types.VM

	BootConfig   *types.BootConfig   `json:"boot_config,omitempty"`    // nil for UEFI boot (cloudimg)
	ImageBlobIDs map[string]struct{} `json:"image_blob_ids,omitempty"` // blob hex set for GC pinning
	FirstBooted  bool                `json:"first_booted,omitempty"`   // true after the VM has been started at least once

	// RunDir and LogDir store the absolute paths used when the VM was created.
	// Persisting them ensures cleanup succeeds even if --run-dir / --log-dir
	// differ from the values at creation time.
	RunDir string `json:"run_dir,omitempty"`
	LogDir string `json:"log_dir,omitempty"`
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
