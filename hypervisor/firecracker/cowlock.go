package firecracker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// withCOWPathLocked takes a flock on a COW disk path, runs fn, then unlocks.
// Serializes access during clone redirect windows. Creates the parent
// directory if needed (source VM may have been deleted).
func withCOWPathLocked(cowPath string, fn func() error) error {
	lockPath := cowPath + ".clone.lock"

	if mkErr := os.MkdirAll(filepath.Dir(lockPath), 0o700); mkErr != nil {
		return fmt.Errorf("create lock dir for %s: %w", lockPath, mkErr)
	}

	fl := flock.New(lockPath)
	if lockErr := fl.Lock(); lockErr != nil {
		return fmt.Errorf("flock %s: %w", lockPath, lockErr)
	}
	// Do NOT remove the lock file after unlock — flock synchronizes on
	// the inode, not the pathname.
	defer func() { _ = fl.Unlock() }()

	return fn()
}
