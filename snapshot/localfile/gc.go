package localfile

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/gc"
	"github.com/cocoonstack/cocoon/lock"
	"github.com/cocoonstack/cocoon/snapshot"
	"github.com/cocoonstack/cocoon/storage"
	"github.com/cocoonstack/cocoon/utils"
)

// pendingGCGrace lets a slow-storage snapshot finish before GC reclaims a pending record.
const pendingGCGrace = 24 * time.Hour

// backfillSizeBytes computes DirSize for records with SizeBytes==0 and persists via WriteRaw.
func backfillSizeBytes(ctx context.Context, conf *Config, store storage.Store[snapshot.SnapshotIndex], records map[string]snapshotMeta) {
	logger := log.WithFunc("localfile.gc.backfillSizeBytes")
	var changed bool
	for id, m := range records {
		if m.sizeBytes > 0 {
			continue
		}
		actual, err := utils.DirSize(conf.SnapshotDataDir(id))
		if err != nil {
			logger.Warnf(ctx, "DirSize for %s: %v", id, err)
			continue
		}
		m.sizeBytes = actual
		records[id] = m
		changed = true
	}
	if !changed {
		return
	}
	if err := store.WriteRaw(func(idx *snapshot.SnapshotIndex) error {
		for id, m := range records {
			if r := idx.Snapshots[id]; r != nil && r.SizeBytes != m.sizeBytes {
				r.SizeBytes = m.sizeBytes
			}
		}
		return nil
	}); err != nil {
		logger.Warnf(ctx, "persist backfilled SizeBytes: %v", err)
	}
}

// EvictionPolicy controls LRU snapshot eviction; Enabled with zero criteria evicts all non-pending.
type EvictionPolicy struct {
	Enabled  bool
	DryRun   bool
	KeepLast int
	MaxAge   time.Duration
	MaxSize  int64
}

func (p EvictionPolicy) hasCriteria() bool {
	return p.KeepLast > 0 || p.MaxAge > 0 || p.MaxSize > 0
}

type snapshotMeta struct {
	name         string
	lastAccessed time.Time
	sizeBytes    int64
}

type snapshotGCSnapshot struct {
	blobIDs      map[string]struct{}
	snapshotIDs  map[string]struct{}
	dataDirs     []string
	stalePending []string
	records      map[string]snapshotMeta
	policy       EvictionPolicy
}

func (s snapshotGCSnapshot) UsedBlobIDs() map[string]struct{} { return s.blobIDs }

func gcModule(conf *Config, store storage.Store[snapshot.SnapshotIndex], locker lock.Locker, policy EvictionPolicy) gc.Module[snapshotGCSnapshot] {
	return gc.Module[snapshotGCSnapshot]{
		Name:   "snapshot",
		Locker: locker,
		ReadDB: func(ctx context.Context) (snapshotGCSnapshot, error) {
			snap := snapshotGCSnapshot{policy: policy}
			cutoff := time.Now().Add(-pendingGCGrace)
			if err := store.ReadRaw(func(idx *snapshot.SnapshotIndex) error {
				snap.blobIDs = make(map[string]struct{})
				snap.snapshotIDs = make(map[string]struct{})
				snap.records = make(map[string]snapshotMeta)
				for id, rec := range idx.Snapshots {
					if rec == nil {
						continue
					}
					snap.snapshotIDs[id] = struct{}{}
					maps.Copy(snap.blobIDs, rec.ImageBlobIDs)
					if rec.Pending {
						if rec.CreatedAt.Before(cutoff) {
							snap.stalePending = append(snap.stalePending, id)
						}
						continue
					}
					snap.records[id] = snapshotMeta{
						name:         rec.Name,
						lastAccessed: rec.LastAccessedAt,
						sizeBytes:    rec.SizeBytes,
					}
				}
				return nil
			}); err != nil {
				return snap, err
			}
			var err error
			if snap.dataDirs, err = utils.ScanSubdirs(conf.DataDir()); err != nil {
				return snap, err
			}
			if policy.MaxSize > 0 {
				backfillSizeBytes(ctx, conf, store, snap.records)
			}
			return snap, nil
		},
		Resolve: func(ctx context.Context, snap snapshotGCSnapshot, _ map[string]any) []string {
			orphans := utils.FilterUnreferenced(snap.dataDirs, snap.snapshotIDs)
			candidates := slices.Concat(orphans, snap.stalePending)

			if snap.policy.Enabled {
				evict := pickLRU(snap.records, snap.policy)
				logEvictions(ctx, evict, snap.records, snap.policy.DryRun)
				if !snap.policy.DryRun {
					candidates = append(candidates, evict...)
				}
			}

			slices.Sort(candidates)
			return slices.Compact(candidates)
		},
		Collect: func(ctx context.Context, ids []string) error {
			var (
				errs    []error
				removed = make([]string, 0, len(ids))
			)
			for _, id := range ids {
				if err := ctx.Err(); err != nil {
					errs = append(errs, err)
					break
				}
				if err := os.RemoveAll(conf.SnapshotDataDir(id)); err != nil {
					errs = append(errs, fmt.Errorf("remove snapshot %s: %w", id, err))
					continue
				}
				removed = append(removed, id)
			}
			if err := cleanResolvedRecords(store, removed); err != nil {
				errs = append(errs, fmt.Errorf("clean DB records: %w", err))
			}
			return errors.Join(errs...)
		},
	}
}

// pickLRU returns evict IDs. No sub-criteria → all records; else union of age/keep/size.
func pickLRU(records map[string]snapshotMeta, p EvictionPolicy) []string {
	sorted := slices.SortedFunc(maps.Keys(records), func(a, b string) int {
		return records[a].lastAccessed.Compare(records[b].lastAccessed)
	})

	if !p.hasCriteria() {
		return sorted
	}

	evict := make(map[string]struct{})

	if p.MaxAge > 0 {
		cutoff := time.Now().Add(-p.MaxAge)
		for _, id := range sorted {
			if records[id].lastAccessed.Before(cutoff) {
				evict[id] = struct{}{}
			}
		}
	}

	if p.KeepLast > 0 && len(sorted) > p.KeepLast {
		for _, id := range sorted[:len(sorted)-p.KeepLast] {
			evict[id] = struct{}{}
		}
	}

	if p.MaxSize > 0 {
		var total int64
		for _, id := range sorted {
			total += records[id].sizeBytes
		}
		for _, id := range sorted {
			if total <= p.MaxSize {
				break
			}
			evict[id] = struct{}{}
			total -= records[id].sizeBytes
		}
	}

	return slices.Sorted(maps.Keys(evict))
}

func logEvictions(ctx context.Context, ids []string, records map[string]snapshotMeta, dryRun bool) {
	if len(ids) == 0 {
		return
	}
	logger := log.WithFunc("localfile.gc.lru")
	verb := "evicting"
	if dryRun {
		verb = "would evict"
	}
	var freed int64
	for _, id := range ids {
		m := records[id]
		freed += m.sizeBytes
		logger.Infof(ctx, "%s id=%s name=%s last_accessed=%s size_bytes=%d",
			verb, id, m.name, formatTime(m.lastAccessed), m.sizeBytes)
	}
	logger.Infof(ctx, "%s %d snapshot(s), freeing %d bytes", verb, len(ids), freed)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.UTC().Format(time.RFC3339)
}

// cleanResolvedRecords drops GC-resolved records; pending only if past grace (protects in-flight Create).
func cleanResolvedRecords(store storage.Store[snapshot.SnapshotIndex], ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-pendingGCGrace)
	return store.WriteRaw(func(idx *snapshot.SnapshotIndex) error {
		utils.CleanStaleRecords(idx.Snapshots, idx.Names, ids,
			func(r *snapshot.SnapshotRecord) string { return r.Name },
			func(r *snapshot.SnapshotRecord) bool {
				if r.Pending {
					return r.CreatedAt.Before(cutoff)
				}
				return true
			},
		)
		return nil
	})
}
