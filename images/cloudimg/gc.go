package cloudimg

import (
	"context"
	"errors"
	"os"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/utils"
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
		ReadDB: func(_ context.Context) (cloudimgSnapshot, error) {
			var snap cloudimgSnapshot
			if err := c.store.Read(func(idx *imageIndex) error {
				snap.refs = images.ReferencedDigests(idx.Images)
				return nil
			}); err != nil {
				return snap, err
			}
			snap.blobs = utils.ScanFileStems(c.conf.CloudimgBlobsDir(), ".qcow2")
			return snap, nil
		},
		Resolve: func(snap cloudimgSnapshot, others map[string]any) []string {
			used := gc.CollectUsedBlobIDs(others)

			// Merge index refs + VM-pinned blobs into one protection set.
			allRefs := make(map[string]struct{}, len(snap.refs)+len(used))
			for k := range snap.refs {
				allRefs[k] = struct{}{}
			}
			for k := range used {
				allRefs[k] = struct{}{}
			}

			return utils.FilterUnreferenced(snap.blobs, allRefs)
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
