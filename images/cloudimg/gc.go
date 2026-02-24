package cloudimg

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/images"
)

// cloudimgSnapshot is the typed GC snapshot for the cloud image backend.
type cloudimgSnapshot struct {
	refs  map[string]struct{} // digest hexes referenced by the index
	blobs []string            // digest hexes of .qcow2 files on disk
}

// GCModule returns a typed gc.Module[cloudimgSnapshot] for the cloud image backend.
func (c *CloudImg) GCModule() gc.Module[cloudimgSnapshot] {
	return gc.Module[cloudimgSnapshot]{
		Name:   typ,
		Locker: c.locker,
		ReadDB: func(ctx context.Context) (cloudimgSnapshot, error) {
			var snap cloudimgSnapshot
			if err := c.store.Read(func(idx *imageIndex) error {
				snap.refs = images.ReferencedDigests(idx.Images)
				return nil
			}); err != nil {
				return snap, err
			}
			if entries, err := os.ReadDir(c.conf.CloudimgBlobsDir()); err == nil {
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), ".qcow2") {
						snap.blobs = append(snap.blobs, strings.TrimSuffix(e.Name(), ".qcow2"))
					}
				}
			}
			return snap, nil
		},
		Resolve: func(snap cloudimgSnapshot, _ map[string]any) []string {
			var unreferenced []string
			for _, hex := range snap.blobs {
				if _, ok := snap.refs[hex]; !ok {
					unreferenced = append(unreferenced, hex)
				}
			}
			return unreferenced
		},
		Collect: func(ctx context.Context, ids []string) error {
			var errs []error
			errs = append(errs, images.GCStaleTemp(ctx, c.conf.CloudimgTempDir(), false)...)
			for _, hex := range ids {
				if err := os.Remove(c.conf.CloudimgBlobPath(hex)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		},
	}
}

// RegisterGC registers the cloud image GC module with the given Orchestrator.
func (c *CloudImg) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, c.GCModule())
}
