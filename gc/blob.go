package gc

// UsedBlobIDs is optionally implemented by GC snapshots that reference image blobs.
// Image GC modules use CollectUsedBlobIDs to skip dangling blobs still needed by VMs.
type UsedBlobIDs interface {
	UsedBlobIDs() map[string]struct{}
}

// CollectUsedBlobIDs aggregates all blob hex IDs referenced by UsedBlobIDs
// snapshots in others.
func CollectUsedBlobIDs(others map[string]any) map[string]struct{} {
	used := make(map[string]struct{})
	for _, snap := range others {
		if u, ok := snap.(UsedBlobIDs); ok {
			for id := range u.UsedBlobIDs() {
				used[id] = struct{}{}
			}
		}
	}
	return used
}
