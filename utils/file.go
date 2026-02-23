package utils

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/projecteru2/core/log"
)

// StaleTempAge is the age threshold for removing stale temp files during GC.
const StaleTempAge = time.Hour

// EnsureDirs creates all directories with 0o750 permissions.
func EnsureDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// ValidFile returns true if path is a regular file with size > 0.
func ValidFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// RemoveMatching scans dir and removes entries where match returns true.
// Returns a slice of errors for entries that could not be removed.
func RemoveMatching(ctx context.Context, dir string, match func(os.DirEntry) bool) []error {
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
			log.WithFunc("gc").Infof(ctx, "GC removed: %s", path)
		}
	}
	return errs
}
