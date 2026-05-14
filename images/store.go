package images

import (
	"github.com/cocoonstack/cocoon/lock"
	"github.com/cocoonstack/cocoon/lock/flock"
	"github.com/cocoonstack/cocoon/storage"
	storejson "github.com/cocoonstack/cocoon/storage/json"
)

// NewStore returns a JSON-backed Store and its locker; both share the same flock file so the locker can be passed to gc.Module independently.
func NewStore[T any](filePath, lockPath string) (storage.Store[T], lock.Locker) {
	locker := flock.New(lockPath)
	return storejson.New[T](filePath, locker), locker
}
