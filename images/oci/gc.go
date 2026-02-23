package oci

import (
	"context"
	"errors"

	"github.com/projecteru2/cocoon/images"
)

// GC removes blobs and boot files that are not referenced by any image in the index,
// and cleans up stale temp directories from interrupted pulls.
func (o *OCI) GC(ctx context.Context) error {
	var errs []error

	// Clean stale temp directories from interrupted pulls (no flock needed).
	errs = append(errs, images.GCStaleTemp(ctx, o.conf.TempDir(), true)...)

	// Clean unreferenced blobs and boot directories under flock.
	if err := o.store.With(ctx, func(idx *imageIndex) error {
		refs := images.ReferencedDigests(idx.Images)
		errs = append(errs, images.GCUnreferencedBlobs(ctx, o.conf.BlobsDir(), ".erofs", refs)...)
		errs = append(errs, images.GCUnreferencedDirs(ctx, o.conf.BootBaseDir(), refs)...)
		return nil
	}); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
