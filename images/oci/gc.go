package oci

import (
	"context"
	"os"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/utils"
)

// ociSnapshot is the typed GC snapshot for the OCI backend.
type ociSnapshot struct {
	refs     map[string]struct{} // digest hexes referenced by the index
	blobs    []string            // digest hexes of .erofs files on disk
	bootDirs []string            // directory names under bootBaseDir on disk
}

// GCModule returns a typed gc.Module[ociSnapshot] for the OCI backend.
func (o *OCI) GCModule() gc.Module[ociSnapshot] {
	return gc.Module[ociSnapshot]{
		Name:   typ,
		Locker: o.locker,
		ReadDB: func(_ context.Context) (ociSnapshot, error) {
			var snap ociSnapshot
			if err := o.store.Read(func(idx *imageIndex) error {
				snap.refs = images.ReferencedDigests(idx.Images)
				return nil
			}); err != nil {
				return snap, err
			}
			var err error
			if snap.blobs, err = utils.ScanFileStems(o.conf.OCIBlobsDir(), ".erofs"); err != nil {
				return snap, err
			}
			if snap.bootDirs, err = utils.ScanSubdirs(o.conf.OCIBootBaseDir()); err != nil {
				return snap, err
			}
			return snap, nil
		},
		Resolve: func(snap ociSnapshot, others map[string]any) []string {
			used := gc.CollectUsedBlobIDs(others)
			allRefs := utils.MergeSets(snap.refs, used)

			candidates := append(
				utils.FilterUnreferenced(snap.blobs, allRefs),
				utils.FilterUnreferenced(snap.bootDirs, allRefs)...,
			)
			// Deduplicate (a hex may appear in both blobs and bootDirs).
			seen := make(map[string]struct{}, len(candidates))
			var result []string
			for _, hex := range candidates {
				if _, ok := seen[hex]; !ok {
					seen[hex] = struct{}{}
					result = append(result, hex)
				}
			}
			return result
		},
		Collect: func(ctx context.Context, ids []string) error {
			return images.GCCollectBlobs(ctx, o.conf.OCITempDir(), true, ids,
				func(hex string) error { return os.Remove(o.conf.BlobPath(hex)) },
				func(hex string) error { return os.RemoveAll(o.conf.BootDir(hex)) },
			)
		},
	}
}

// RegisterGC registers the OCI GC module with the given Orchestrator.
func (o *OCI) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, o.GCModule())
}
