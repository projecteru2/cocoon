package oci

import (
	"context"
	"errors"
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
			snap.blobs = utils.ScanFileStems(o.conf.OCIBlobsDir(), ".erofs")
			snap.bootDirs = utils.ScanSubdirs(o.conf.OCIBootBaseDir())
			return snap, nil
		},
		Resolve: func(snap ociSnapshot, others map[string]any) []string {
			used := gc.CollectUsedBlobIDs(others)

			// Merge index refs + VM-pinned blobs into one protection set.
			allRefs := make(map[string]struct{}, len(snap.refs)+len(used))
			for k := range snap.refs {
				allRefs[k] = struct{}{}
			}
			for k := range used {
				allRefs[k] = struct{}{}
			}

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
			var errs []error
			errs = append(errs, images.GCStaleTemp(ctx, o.conf.OCITempDir(), true)...)
			for _, hex := range ids {
				if err := os.Remove(o.conf.BlobPath(hex)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
				if err := os.RemoveAll(o.conf.BootDir(hex)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		},
	}
}

// RegisterGC registers the OCI GC module with the given Orchestrator.
func (o *OCI) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, o.GCModule())
}
