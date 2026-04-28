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

// SnapshotMetaFile is the cocoon-owned sidecar carrying fields the hypervisor's
// native config can't hold (Role/MountPoint/FSType/DirectIO; FC CPU/Memory).
const SnapshotMetaFile = "cocoon.json"

type SnapshotMeta struct {
	StorageConfigs []*types.StorageConfig `json:"storage_configs"`
	BootConfig     *types.BootConfig      `json:"boot_config,omitempty"`
	CPU            int                    `json:"cpu,omitempty"`
	Memory         int64                  `json:"memory,omitempty"`
}

func SaveSnapshotMeta(dir string, meta *SnapshotMeta) error {
	return utils.AtomicWriteJSON(filepath.Join(dir, SnapshotMetaFile), meta)
}

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

func CloneStorageConfigs(storageConfigs []*types.StorageConfig) []*types.StorageConfig {
	out := make([]*types.StorageConfig, 0, len(storageConfigs))
	for _, sc := range storageConfigs {
		cp := *sc
		out = append(out, &cp)
	}
	return out
}

// IsUnderDir reports whether path is strictly under dir. An empty dir returns
// false (disables the check) rather than matching every path.
func IsUnderDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	cleaned := filepath.Clean(path)
	root := filepath.Clean(dir)
	return strings.HasPrefix(cleaned, root+string(filepath.Separator))
}

// ValidateMetaPaths rejects sidecar paths escaping cocoon-managed roots; an
// imported snapshot's cocoon.json is otherwise untrusted.
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

// ReverseLayers projects Role==Layer entries through fn in reverse order
// (topmost layer first, matching overlayfs lowerdir semantics).
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
