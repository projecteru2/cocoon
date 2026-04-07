package firecracker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// lockCOWPath takes a flock on a COW disk path to serialize access during
// clone redirect windows. Creates the parent directory if needed (source VM
// may have been deleted). Returns an unlock function.
func lockCOWPath(cowPath string) (unlock func(), err error) {
	noop := func() {}
	lockPath := cowPath + ".clone.lock"

	if mkErr := os.MkdirAll(filepath.Dir(lockPath), 0o700); mkErr != nil {
		return noop, fmt.Errorf("create lock dir for %s: %w", lockPath, mkErr)
	}

	fl := flock.New(lockPath)
	if lockErr := fl.Lock(); lockErr != nil {
		return noop, fmt.Errorf("flock %s: %w", lockPath, lockErr)
	}

	// Do NOT remove the lock file after unlock. flock synchronizes on
	// the inode: removing the file while other callers are blocked would
	// let a third caller create a new file (different inode) and acquire
	// it immediately, defeating serialization. The lock file is small
	// and harmless to leave on disk.
	return func() {
		_ = fl.Unlock()
	}, nil
}
