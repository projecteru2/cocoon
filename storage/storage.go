package storage

import "context"

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
}
