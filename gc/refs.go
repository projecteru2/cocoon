// Cross-module GC protocols: snapshots opt in via the matching method; this file imports nothing concrete.
package gc

// usedBlobIDs is implemented by snapshots that reference image blobs.
type usedBlobIDs interface {
	UsedBlobIDs() map[string]struct{}
}

// activeVMIDs is implemented by snapshots that track live VMs.
type activeVMIDs interface {
	ActiveVMIDs() map[string]struct{}
}

// Collect aggregates ID sets from snapshots via accessor; snapshots that don't implement it are skipped.
func Collect(others map[string]any, accessor func(any) map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{})
	for _, snap := range others {
		for id := range accessor(snap) {
			result[id] = struct{}{}
		}
	}
	return result
}

// BlobIDs extracts blob hex IDs from a snapshot.
// Returns nil if the snapshot does not implement UsedBlobIDs.
func BlobIDs(snap any) map[string]struct{} {
	if u, ok := snap.(usedBlobIDs); ok {
		return u.UsedBlobIDs()
	}
	return nil
}

// VMIDs extracts active VM IDs from a snapshot.
// Returns nil if the snapshot does not implement ActiveVMIDs.
func VMIDs(snap any) map[string]struct{} {
	if a, ok := snap.(activeVMIDs); ok {
		return a.ActiveVMIDs()
	}
	return nil
}
