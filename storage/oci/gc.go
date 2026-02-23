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
	return errors.Join(o.gcUnreferenced(ctx)...)
}

// gcUnreferenced removes blobs and boot directories not referenced by any image,
// and cleans up stale temp directories.
func (o *OCI) gcUnreferenced(ctx context.Context) []error {
	var errs []error

	// Clean stale temp directories from interrupted pulls (no flock needed).
	errs = append(errs, o.gcStaleTempDirs(ctx)...)

	if err := o.idx.With(ctx, func(idx *imageIndex) error {
		referenced := idx.referencedDigests()
		logger := log.WithFunc("oci.gc")

		// Clean unreferenced blobs.
		entries, err := os.ReadDir(o.conf.BlobsDir())
		if err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("read blobs dir: %w", err))
		}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".erofs") {
				continue
			}
			hex := strings.TrimSuffix(n, ".erofs")
			if _, ok := referenced[hex]; !ok {
				if err := os.Remove(filepath.Join(o.conf.BlobsDir(), n)); err != nil {
					errs = append(errs, fmt.Errorf("remove blob %s: %w", n, err))
				} else {
					logger.Infof(ctx, "Removed unreferenced blob: %s", n)
				}
			}
		}

		// Clean unreferenced boot directories.
		entries, err = os.ReadDir(o.conf.BootBaseDir())
		if err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("read boot dir: %w", err))
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			hex := e.Name()
			if _, ok := referenced[hex]; !ok {
				if err := os.RemoveAll(filepath.Join(o.conf.BootBaseDir(), hex)); err != nil {
					errs = append(errs, fmt.Errorf("remove boot dir %s: %w", hex, err))
				} else {
					logger.Infof(ctx, "Removed unreferenced boot dir: %s", hex)
				}
			}
		}
		return nil
	}); err != nil {
		errs = append(errs, err)
	}
	return errs
}

// gcStaleTempDirs removes temp directories older than staleTempAge.
// These are left behind by interrupted pull operations (e.g., SIGKILL, power failure).
func (o *OCI) gcStaleTempDirs(ctx context.Context) []error {
	var errs []error
	entries, err := os.ReadDir(o.conf.TempDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []error{fmt.Errorf("read temp dir: %w", err)}
	}

	logger := log.WithFunc("oci.gc")
	cutoff := time.Now().Add(-staleTempAge)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(o.conf.TempDir(), e.Name())
			if err := os.RemoveAll(path); err != nil {
				errs = append(errs, fmt.Errorf("remove stale temp dir %s: %w", e.Name(), err))
			} else {
				logger.Infof(ctx, "Removed stale temp dir: %s", e.Name())
			}
		}
	}
	return errs
}
