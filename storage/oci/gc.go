package oci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/projecteru2/core/log"
)

const staleTempAge = time.Hour

// GC removes blobs and boot files that are not referenced by any image in the index,
// and cleans up stale temp directories from interrupted pulls.
func (o *OCI) GC(ctx context.Context) error {
	var errs []error

	// Clean stale temp directories from interrupted pulls (no flock needed).
	cutoff := time.Now().Add(-staleTempAge)
	errs = append(errs, removeMatching(ctx, o.conf.TempDir(), func(e os.DirEntry) bool {
		if !e.IsDir() {
			return false
		}
		info, err := e.Info()
		return err == nil && info.ModTime().Before(cutoff)
	})...)

	// Clean unreferenced blobs and boot directories under flock.
	if err := o.idx.With(ctx, func(idx *imageIndex) error {
		ref := idx.referencedDigests()

		errs = append(errs, removeMatching(ctx, o.conf.BlobsDir(), func(e os.DirEntry) bool {
			n := e.Name()
			if !strings.HasSuffix(n, ".erofs") {
				return false
			}
			_, ok := ref[strings.TrimSuffix(n, ".erofs")]
			return !ok
		})...)

		errs = append(errs, removeMatching(ctx, o.conf.BootBaseDir(), func(e os.DirEntry) bool {
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

// removeMatching scans dir and removes entries where match returns true.
func removeMatching(ctx context.Context, dir string, match func(os.DirEntry) bool) []error {
	logger := log.WithFunc("oci.gc")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []error{fmt.Errorf("read %s: %w", dir, err)}
	}

	var errs []error
	for _, e := range entries {
		if !match(e) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
		} else {
			logger.Infof(ctx, "GC removed: %s", path)
		}
	}
	return errs
}
