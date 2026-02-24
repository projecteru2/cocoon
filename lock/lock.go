package lock

import "context"

// Locker provides mutual exclusion with context support.
type Locker interface {
	Lock(ctx context.Context) error
	Unlock(ctx context.Context) error
	TryLock(ctx context.Context) (bool, error)
}

