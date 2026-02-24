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

// Lock provides mutual exclusion combining:
//   - In-process exclusion via a size-1 buffered channel. A goroutine acquires
//     the in-process token by sending to ch; it releases by receiving from ch.
//     Using a channel (rather than sync.Mutex) enables context-aware blocking in
//     Lock() and non-blocking short-circuit in TryLock() without any syscall.
//   - Cross-process exclusion via flock(2) with a fresh fd on every acquisition,
//     so concurrent callers on the same Lock instance properly block each other.
type Lock struct {
	path string
	ch   chan struct{}
	// fl is the active flock fd, non-nil while the lock is held.
	fl *flock.Flock
}

// New creates a Lock for the given path.
func New(path string) *Lock {
	return &Lock{path: path, ch: make(chan struct{}, 1)}
}

// Lock acquires the lock, blocking until available or ctx is cancelled.
func (l *Lock) Lock(ctx context.Context) error {
	select {
	case l.ch <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("acquire lock %s: %w", l.path, ctx.Err())
	}
	ok, err := l.commitFlock(func(fl *flock.Flock) (bool, error) {
		return fl.TryLockContext(ctx, retryDelay)
	})
	if err != nil {
		return fmt.Errorf("acquire flock %s: %w", l.path, err)
	}
	if !ok {
		return fmt.Errorf("acquire flock %s: %w", l.path, ctx.Err())
	}
	return nil
}

// TryLock attempts a non-blocking acquisition.
// Returns (false, nil) if the lock is currently held by another caller.
func (l *Lock) TryLock(_ context.Context) (bool, error) {
	select {
	case l.ch <- struct{}{}:
	default:
		return false, nil
	}
	return l.commitFlock(func(fl *flock.Flock) (bool, error) {
		return fl.TryLock()
	})
}

// Unlock releases the lock.
func (l *Lock) Unlock(_ context.Context) error {
	var err error
	if l.fl != nil {
		err = l.fl.Unlock()
		l.fl = nil
	}
	select {
	case <-l.ch:
	default:
	}
	if err != nil {
		return fmt.Errorf("release flock %s: %w", l.path, err)
	}
	return nil
}

// commitFlock opens a fresh flock fd, runs acquire, and either stores the fd
// (on success) or releases the channel token (on failure) so Unlock is always
// called in a balanced pair with Lock/TryLock.
func (l *Lock) commitFlock(acquire func(*flock.Flock) (bool, error)) (bool, error) {
	fl := flock.New(l.path)
	locked, err := acquire(fl)
	if err != nil {
		<-l.ch
		return false, err
	}
	if !locked {
		<-l.ch
		return false, nil
	}
	l.fl = fl
	return true, nil
}
