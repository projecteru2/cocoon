package cloudimg

import (
	"context"
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
			var err error
			if snap.blobs, err = utils.ScanFileStems(c.conf.BlobsDir(), ".qcow2"); err != nil {
				return snap, err
			}
			return snap, nil
		},
		Resolve: func(snap cloudimgSnapshot, others map[string]any) []string {
			used := gc.Collect(others, gc.BlobIDs)
			allRefs := utils.MergeSets(snap.refs, used)
			return utils.FilterUnreferenced(snap.blobs, allRefs)
		},
		Collect: func(ctx context.Context, ids []string) error {
			return images.GCCollectBlobs(ctx, c.conf.TempDir(), false, ids,
				func(hex string) error { return os.Remove(c.conf.BlobPath(hex)) },
			)
		},
	}
}

// RegisterGC registers the cloud image GC module with the given Orchestrator.
func (c *CloudImg) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, c.GCModule())
}
