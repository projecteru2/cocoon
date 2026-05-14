package utils

// CleanStaleRecords removes targetIDs from items, re-checking isStale to dodge TOCTOU; nameOf=="" skips nameMap cleanup.
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
