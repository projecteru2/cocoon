package images

import (
	"github.com/cocoonstack/cocoon/lock"
	"github.com/cocoonstack/cocoon/lock/flock"
	"github.com/cocoonstack/cocoon/storage"
	storejson "github.com/cocoonstack/cocoon/storage/json"
)

// NewStore creates a JSON-backed Store and returns it alongside the locker.
// Both use the same underlying flock so the locker can be passed independently
// (e.g. to gc.Module) while sharing the same cross-process lock file.
func NewStore[T any](filePath, lockPath string) (storage.Store[T], lock.Locker) {
	locker := flock.New(lockPath)
	return storejson.New[T](filePath, locker), locker
}
