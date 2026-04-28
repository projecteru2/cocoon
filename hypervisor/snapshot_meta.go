package hypervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// LoadAndValidateMeta loads the sidecar from dir and rejects paths that
// escape rootDir or runDir. Both backends use this entry point so an
// imported snapshot's cocoon.json cannot smuggle paths outside cocoon-managed
// roots before any subsequent open/rename operation runs.
func LoadAndValidateMeta(dir, rootDir, runDir string) (*SnapshotMeta, error) {
	meta, err := LoadSnapshotMeta(dir)
	if err != nil {
		return nil, err
	}
	if err := ValidateMetaPaths(meta, rootDir, runDir); err != nil {
		return nil, err
	}
	return meta, nil
}

// CloneStorageConfigs returns a deep copy of storageConfigs suitable for
// embedding in an on-disk sidecar — subsequent mutations on the live VM
// record won't taint the persisted JSON.
func CloneStorageConfigs(storageConfigs []*types.StorageConfig) []*types.StorageConfig {
	out := make([]*types.StorageConfig, 0, len(storageConfigs))
	for _, sc := range storageConfigs {
		cp := *sc
		out = append(out, &cp)
	}
	return out
}

// IsUnderDir reports whether path resolves to a location strictly under dir.
// Used to reject snapshot-imported paths that escape cocoon-managed roots.
// An empty dir always returns false to disable the check rather than match
// every path.
func IsUnderDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	cleaned := filepath.Clean(path)
	root := filepath.Clean(dir)
	return strings.HasPrefix(cleaned, root+string(filepath.Separator))
}

// ValidateMetaPaths rejects sidecar paths that escape cocoon-managed roots.
// Snapshots may be imported across hosts with arbitrary cocoon.json content,
// so every path is checked before subsequent code opens the file.
func ValidateMetaPaths(meta *SnapshotMeta, rootDir, runDir string) error {
	for _, sc := range meta.StorageConfigs {
		if !IsUnderDir(sc.Path, rootDir) && !IsUnderDir(sc.Path, runDir) {
			return fmt.Errorf("untrusted storage path in snapshot metadata: %s", sc.Path)
		}
	}
	if b := meta.BootConfig; b != nil {
		if b.KernelPath != "" && !IsUnderDir(b.KernelPath, rootDir) {
			return fmt.Errorf("untrusted kernel path in snapshot metadata: %s", b.KernelPath)
		}
		if b.InitrdPath != "" && !IsUnderDir(b.InitrdPath, rootDir) {
			return fmt.Errorf("untrusted initrd path in snapshot metadata: %s", b.InitrdPath)
		}
	}
	return nil
}

// ReverseLayers walks storageConfigs once, collecting Role==Layer entries in
// reverse order via project. The reversed result mirrors overlayfs lowerdir
// semantics where the topmost layer comes first.
func ReverseLayers[T any](storageConfigs []*types.StorageConfig, project func(idx int, sc *types.StorageConfig) T) []T {
	var layers []*types.StorageConfig
	for _, sc := range storageConfigs {
		if sc.Role == types.StorageRoleLayer {
			layers = append(layers, sc)
		}
	}
	out := make([]T, len(layers))
	for i, sc := range layers {
		out[len(layers)-1-i] = project(i, sc)
	}
	return out
}
