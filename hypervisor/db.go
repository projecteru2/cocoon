package hypervisor

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/projecteru2/cocoon/types"
)

// VMRecord is the persisted record for a single VM.
// It extends types.VMInfo with the disk and boot configuration needed to
// restart a VM without re-resolving the image.
//
// PID and SocketPath on types.VMInfo are NOT stored here:
//   - SocketPath is deterministic: derived from config at query time.
//   - PID changes on every start; Inspect reads the live value from the PID
//     file instead, avoiding stale PIDs after a crash or reboot.
type VMRecord struct {
	types.VMInfo

	// StorageConfigs holds the ordered disk attachments at creation time
	// (EROFS layers first, then the COW disk). Persisted so that a stopped
	// VM can be restarted with the same disk layout.
	StorageConfigs []*types.StorageConfig `json:"storage_configs"`

	// BootConfig holds the kernel and initrd paths for direct-boot VMs.
	// Nil for UEFI-boot VMs (cloud images).
	BootConfig *types.BootConfig `json:"boot_config,omitempty"`

	// ImageBlobIDs is the set of digest hex strings of image blobs this VM
	// depends on. Populated at create time from the original StorageConfigs
	// (before COW prepare). Image GC skips dangling blobs that appear here.
	ImageBlobIDs map[string]struct{} `json:"image_blob_ids,omitempty"`
}

// VMIndex is the top-level DB structure shared by all hypervisor backends.
// Each backend stores its VMs under a separate index file
// (e.g. {RootDir}/cloudhypervisor/db/vms.json).
type VMIndex struct {
	VMs   map[string]*VMRecord `json:"vms"`
	Names map[string]string    `json:"names"` // name → VM ID
}

// Init implements storage.Initer — initialises nil maps after deserialization.
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

// ResolveVMRef resolves a user-supplied reference (exact ID, name, or ID prefix)
// to a full VM ID. Resolution order: exact ID → name → ID prefix (≥3 chars).
func ResolveVMRef(idx *VMIndex, ref string) (string, error) {
	// 1. Exact ID match.
	if idx.VMs[ref] != nil {
		return ref, nil
	}
	// 2. Name index match.
	if id, ok := idx.Names[ref]; ok && idx.VMs[id] != nil {
		return id, nil
	}
	// 3. ID prefix match (require ≥3 chars to avoid overly broad matches).
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
