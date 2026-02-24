package hypervisor

import "github.com/projecteru2/cocoon/types"

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
}

// VMIndex is the top-level DB structure shared by all hypervisor backends.
// Each backend stores its VMs under a separate index file
// (e.g. {RootDir}/cloudhypervisor/db/vms.json).
type VMIndex struct {
	VMs map[string]*VMRecord `json:"vms"`
}

// Init implements storage.Initer — initialises the nil map after deserialization.
func (idx *VMIndex) Init() {
	if idx.VMs == nil {
		idx.VMs = make(map[string]*VMRecord)
	}
}
