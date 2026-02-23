package cloudimg

import (
	"context"
	"errors"

	"github.com/projecteru2/cocoon/images"
)

// GC removes blobs that are not referenced by any image in the index,
// and cleans up stale temp files from interrupted pulls.
func (c *CloudImg) GC(ctx context.Context) error {
	var errs []error

	// Clean stale temp files from interrupted pulls (no flock needed).
	errs = append(errs, images.GCStaleTemp(ctx, c.conf.CloudimgTempDir(), false)...)

	// Clean unreferenced blobs under flock.
	if err := c.store.With(ctx, func(idx *imageIndex) error {
		refs := images.ReferencedDigests(idx.Images)
		errs = append(errs, images.GCUnreferencedBlobs(ctx, c.conf.CloudimgBlobsDir(), ".qcow2", refs)...)
		return nil
	}); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
