package oci

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/projecteru2/cocoon/utils"
)

// GC removes blobs and boot files that are not referenced by any image in the index,
// and cleans up stale temp directories from interrupted pulls.
func (o *OCI) GC(ctx context.Context) error {
	var errs []error

	// Clean stale temp directories from interrupted pulls (no flock needed).
	cutoff := time.Now().Add(-utils.StaleTempAge)
	errs = append(errs, utils.RemoveMatching(ctx, o.conf.TempDir(), func(e os.DirEntry) bool {
		if !e.IsDir() {
			return false
		}
		info, err := e.Info()
		return err == nil && info.ModTime().Before(cutoff)
	})...)

	// Clean unreferenced blobs and boot directories under flock.
	if err := o.store.With(ctx, func(idx *imageIndex) error {
		ref := idx.referencedDigests()

		errs = append(errs, utils.RemoveMatching(ctx, o.conf.BlobsDir(), func(e os.DirEntry) bool {
			n := e.Name()
			if !strings.HasSuffix(n, ".erofs") {
				return false
			}
			_, ok := ref[strings.TrimSuffix(n, ".erofs")]
			return !ok
		})...)

		errs = append(errs, utils.RemoveMatching(ctx, o.conf.BootBaseDir(), func(e os.DirEntry) bool {
			if !e.IsDir() {
				return false
			}
			_, ok := ref[e.Name()]
			return !ok
		})...)

		return nil
	}); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
