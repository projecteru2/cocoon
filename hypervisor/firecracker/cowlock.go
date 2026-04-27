package firecracker

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/gofrs/flock"

	"github.com/cocoonstack/cocoon/types"
)

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

// withSourceWritableDisksLocked locks every writable disk path of a source
// VM (COW + every data disk) in dictionary order before invoking fn. The
// fixed ordering means concurrent clones / snapshots of the same VM never
// deadlock against each other.
//
// Each lock acquisition runs recoverStaleBackup so an interrupted previous
// clone of that path can finish swapping its symlink redirect before the
// next caller proceeds.
func withSourceWritableDisksLocked(configs []*types.StorageConfig, fn func() error) error {
	paths := make([]string, 0, len(configs))
	for _, sc := range configs {
		if sc.Role == types.StorageRoleCOW || sc.Role == types.StorageRoleData {
			paths = append(paths, sc.Path)
		}
	}
	slices.Sort(paths)
	return withPathsLocked(paths, fn)
}

func withPathsLocked(paths []string, fn func() error) error {
	if len(paths) == 0 {
		return fn()
	}
	return withCOWPathLocked(paths[0], func() error {
		recoverStaleBackup(paths[0])
		return withPathsLocked(paths[1:], fn)
	})
}
