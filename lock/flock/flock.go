package flock

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/flock"

	"github.com/projecteru2/cocoon/lock"
)

const retryDelay = 100 * time.Millisecond

// compile-time interface check.
var _ lock.Locker = (*Lock)(nil)

// Lock provides cross-process mutual exclusion using flock(2) via gofrs/flock.
// Lock files are long-lived and never deleted after use.
type Lock struct {
	fl *flock.Flock
}

// New creates a new Lock for the given path.
func New(path string) *Lock {
	return &Lock{fl: flock.New(path)}
}

// Lock acquires an exclusive flock. Blocks until the lock is available
// or the context is cancelled.
func (l *Lock) Lock(ctx context.Context) error {
	locked, err := l.fl.TryLockContext(ctx, retryDelay)
	if err != nil {
		return fmt.Errorf("acquire flock %s: %w", l.fl.Path(), err)
	}
	if !locked {
		return fmt.Errorf("failed to acquire flock %s: context done", l.fl.Path())
	}
	return nil
}

// Unlock releases the flock.
func (l *Lock) Unlock(_ context.Context) error {
	if err := l.fl.Unlock(); err != nil {
		return fmt.Errorf("release flock %s: %w", l.fl.Path(), err)
	}
	return nil
}
