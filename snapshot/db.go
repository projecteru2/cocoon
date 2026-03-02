package snapshot

import (
	"errors"
	"fmt"
	"strings"

	"github.com/projecteru2/cocoon/types"
)

var ErrNotFound = errors.New("snapshot not found")

// SnapshotRecord is the persisted record for a single snapshot.
type SnapshotRecord struct {
	types.Snapshot
	ImageBlobIDs map[string]struct{} `json:"image_blob_ids,omitempty"` // blob hex set for GC pinning
	DataDir      string              `json:"data_dir,omitempty"`
}

// SnapshotIndex is the top-level DB structure for the snapshot module.
type SnapshotIndex struct {
	Snapshots map[string]*SnapshotRecord `json:"snapshots"`
	Names     map[string]string          `json:"names"` // name → snapshot ID
}

// Init implements storage.Initer.
func (idx *SnapshotIndex) Init() {
	if idx.Snapshots == nil {
		idx.Snapshots = make(map[string]*SnapshotRecord)
	}
	if idx.Names == nil {
		idx.Names = make(map[string]string)
	}
}

// ResolveSnapshotRef resolves a ref (exact ID, name, or ID prefix ≥3 chars) to a full snapshot ID.
func ResolveSnapshotRef(idx *SnapshotIndex, ref string) (string, error) {
	if idx.Snapshots[ref] != nil {
		return ref, nil
	}
	if id, ok := idx.Names[ref]; ok && idx.Snapshots[id] != nil {
		return id, nil
	}
	if len(ref) >= 3 {
		var match string
		for id := range idx.Snapshots {
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
