package cloudimg

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/projecteru2/cocoon/utils"
)

// GC removes blobs that are not referenced by any image in the index,
// and cleans up stale temp files from interrupted pulls.
func (c *CloudImg) GC(ctx context.Context) error {
	var errs []error

	// Clean stale temp files from interrupted pulls (no flock needed).
	cutoff := time.Now().Add(-utils.StaleTempAge)
	errs = append(errs, utils.RemoveMatching(ctx, c.conf.CloudimgTempDir(), func(e os.DirEntry) bool {
		info, err := e.Info()
		return err == nil && info.ModTime().Before(cutoff)
	})...)

	// Clean unreferenced blobs under flock.
	if err := c.store.With(ctx, func(idx *imageIndex) error {
		ref := idx.referencedDigests()

		errs = append(errs, utils.RemoveMatching(ctx, c.conf.CloudimgBlobsDir(), func(e os.DirEntry) bool {
			n := e.Name()
			if !strings.HasSuffix(n, ".qcow2") {
				return false
			}
			_, ok := ref[strings.TrimSuffix(n, ".qcow2")]
			return !ok
		})...)

		return nil
	}); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
