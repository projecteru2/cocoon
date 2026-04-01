package images

import (
	"context"
	"slices"

	"github.com/cocoonstack/cocoon/gc"
	"github.com/cocoonstack/cocoon/lock"
	"github.com/cocoonstack/cocoon/storage"
	"github.com/cocoonstack/cocoon/utils"
)

// ImageGCSnapshot is the unified GC snapshot for image backends.
type ImageGCSnapshot struct {
	refs    map[string]struct{} // digest hexes referenced by the index
	diskIDs []string            // digest hexes found on disk (blobs + optional extras)
}

// GCModuleConfig configures a generic image GC module.
type GCModuleConfig[I any] struct {
	Name   string
	Locker lock.Locker
	Store  storage.Store[I]
	// ReadRefs extracts referenced digest hexes from the index.
	ReadRefs func(*I) map[string]struct{}
	// ScanDisk returns digest hexes found on disk (blobs).
	ScanDisk func() ([]string, error)
	// ExtraDisk returns additional hex IDs on disk (e.g., OCI boot dirs). Optional.
	ExtraDisk func() ([]string, error)
	// Removers are called per hex ID during collect.
	Removers []func(string) error
	// TempDir for stale temp cleanup.
	TempDir string
	// DirOnly: true for OCI (temp dirs), false for cloudimg (temp files).
	DirOnly bool
}

// BuildGCModule constructs a gc.Module from the config.
// This eliminates the near-identical GCModule() methods in oci/ and cloudimg/.
func BuildGCModule[I any](cfg GCModuleConfig[I]) gc.Module[ImageGCSnapshot] {
	return gc.Module[ImageGCSnapshot]{
		Name:   cfg.Name,
		Locker: cfg.Locker,
		ReadDB: func(_ context.Context) (ImageGCSnapshot, error) {
			var snap ImageGCSnapshot
			if err := cfg.Store.ReadRaw(func(idx *I) error {
				snap.refs = cfg.ReadRefs(idx)
				return nil
			}); err != nil {
				return snap, err
			}
			var err error
			if snap.diskIDs, err = cfg.ScanDisk(); err != nil {
				return snap, err
			}
			if cfg.ExtraDisk != nil {
				extra, err := cfg.ExtraDisk()
				if err != nil {
					return snap, err
				}
				snap.diskIDs = append(snap.diskIDs, extra...)
			}
			return snap, nil
		},
		Resolve: func(snap ImageGCSnapshot, others map[string]any) []string {
			used := gc.Collect(others, gc.BlobIDs)
			allRefs := utils.MergeSets(snap.refs, used)
			candidates := utils.FilterUnreferenced(snap.diskIDs, allRefs)
			slices.Sort(candidates)
			return slices.Compact(candidates)
		},
		Collect: func(ctx context.Context, ids []string) error {
			return GCCollectBlobs(ctx, cfg.TempDir, cfg.DirOnly, ids, cfg.Removers...)
		},
	}
}
