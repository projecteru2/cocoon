package oci

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/images"
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
		ReadDB: func(ctx context.Context) (ociSnapshot, error) {
			var snap ociSnapshot
			if err := o.store.Read(func(idx *imageIndex) error {
				snap.refs = images.ReferencedDigests(idx.Images)
				return nil
			}); err != nil {
				return snap, err
			}
			if entries, err := os.ReadDir(o.conf.OCIBlobsDir()); err == nil {
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), ".erofs") {
						snap.blobs = append(snap.blobs, strings.TrimSuffix(e.Name(), ".erofs"))
					}
				}
			}
			if entries, err := os.ReadDir(o.conf.OCIBootBaseDir()); err == nil {
				for _, e := range entries {
					if e.IsDir() {
						snap.bootDirs = append(snap.bootDirs, e.Name())
					}
				}
			}
			return snap, nil
		},
		Resolve: func(snap ociSnapshot, _ map[string]any) []string {
			seen := make(map[string]struct{})
			var unreferenced []string
			for _, hex := range snap.blobs {
				if _, ok := snap.refs[hex]; !ok {
					unreferenced = append(unreferenced, hex)
					seen[hex] = struct{}{}
				}
			}
			for _, hex := range snap.bootDirs {
				if _, ok := snap.refs[hex]; !ok {
					if _, already := seen[hex]; !already {
						unreferenced = append(unreferenced, hex)
					}
				}
			}
			return unreferenced
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
