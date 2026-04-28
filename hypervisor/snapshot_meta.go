package hypervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// SnapshotMetaFile is the cocoon-owned sidecar persisted alongside the
// hypervisor's native snapshot files. It carries data the hypervisor's
// own config schema cannot hold (Role/MountPoint/FSType/DirectIO on disks)
// and FC-specific resource numbers (CPU/Memory) that are not in the
// vmstate binary.
const SnapshotMetaFile = "cocoon.json"

// SnapshotMeta is the on-disk shape of the cocoon sidecar. CH leaves
// CPU/Memory zero (omitempty drops them); FC fills them in because FC
// snapshot/load cannot resize the guest after restore.
type SnapshotMeta struct {
	StorageConfigs []*types.StorageConfig `json:"storage_configs"`
	BootConfig     *types.BootConfig      `json:"boot_config,omitempty"`
	CPU            int                    `json:"cpu,omitempty"`
	Memory         int64                  `json:"memory,omitempty"`
}

// SaveSnapshotMeta atomically writes meta to dir/SnapshotMetaFile. Atomic
// rename matters: clone/restore later read the sidecar after this write,
// and a partial file from a crashed cocoon would surface as a JSON decode
// error blocking those flows.
func SaveSnapshotMeta(dir string, meta *SnapshotMeta) error {
	return utils.AtomicWriteJSON(filepath.Join(dir, SnapshotMetaFile), meta)
}

// LoadSnapshotMeta reads and decodes dir/SnapshotMetaFile.
func LoadSnapshotMeta(dir string) (*SnapshotMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, SnapshotMetaFile)) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", SnapshotMetaFile, err)
	}
	var meta SnapshotMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("decode %s: %w", SnapshotMetaFile, err)
	}
	return &meta, nil
}
