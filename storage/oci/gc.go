package oci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/projecteru2/core/log"
)

// GC removes blobs and boot files that are not referenced by any image in the index.
func (o *OCI) GC(ctx context.Context) error {
	return errors.Join(o.gcUnreferenced(ctx)...)
}

// gcUnreferenced removes blobs and boot directories not referenced by any image.
func (o *OCI) gcUnreferenced(ctx context.Context) []error {
	var errs []error
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
