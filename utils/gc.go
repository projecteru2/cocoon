package utils

// CleanStaleRecords removes records matching targetIDs from items,
// with a staleness re-check via isStale to guard against TOCTOU.
// nameOf extracts the name for nameMap cleanup ("" means no name entry).
func CleanStaleRecords[T any](
	items map[string]*T,
	nameMap map[string]string,
	targetIDs []string,
	nameOf func(*T) string,
	isStale func(*T) bool,
) {
	for _, id := range targetIDs {
		rec := items[id]
		if rec == nil || !isStale(rec) {
			continue
		}
		if n := nameOf(rec); n != "" {
			delete(nameMap, n)
		}
		delete(items, id)
	}
}
