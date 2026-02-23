package lock

import "context"

// Locker provides mutual exclusion with context support.
type Locker interface {
	Lock(ctx context.Context) error
	Unlock(ctx context.Context) error
}

// WithLock acquires the lock, calls fn, and releases the lock.
// If fn returns an error, the lock is still released.
func WithLock(ctx context.Context, l Locker, fn func() error) error {
	if err := l.Lock(ctx); err != nil {
		return err
	}
	defer l.Unlock(ctx) //nolint:errcheck
	return fn()
}
