package storage

import (
	"context"
)

// Initer is optionally implemented by T to initialize zero-value fields
// (e.g., nil maps) after deserialization or when the backing store is empty.
type Initer interface {
	Init()
}

// Store provides locked read/modify/write access to a data store.
// T is the top-level structure managed by the store.
type Store[T any] interface {
	// With loads the data under lock and passes it to fn.
	// If *T implements Initer, Init() is called before fn.
	// The lock is held for the duration of fn.
	With(ctx context.Context, fn func(*T) error) error
	// Update performs a read-modify-write under lock.
	// If fn returns nil the data is persisted.
	Update(ctx context.Context, fn func(*T) error) error

	// Read deserializes the data and passes it to fn without acquiring the lock.
	// The caller must already hold the lock via TryLock.
	Read(fn func(*T) error) error
	// Write deserializes the data, passes it to fn, and atomically persists the
	// result if fn returns nil. Does not acquire the lock.
	// The caller must already hold the lock via TryLock.
	Write(fn func(*T) error) error
	// TryLock attempts to acquire the lock without blocking.
	// Returns (false, nil) if currently held by another caller.
	// On success (true, nil) the caller must call Unlock when done.
	TryLock(ctx context.Context) (bool, error)
	// Unlock releases a lock previously acquired by TryLock.
	Unlock(ctx context.Context) error
}
