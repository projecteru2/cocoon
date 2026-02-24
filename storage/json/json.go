package json

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/storage"
	"github.com/projecteru2/cocoon/utils"
)

// compile-time interface check.
var _ storage.Store[struct{}] = (*Store[struct{}])(nil)

// Store provides locked read/modify/write access to a JSON file.
// T is the top-level structure stored in the file (must have exported fields with json tags).
// If *T implements storage.Initer, Init() is called automatically after loading.
// The caller provides the Locker, decoupling storage from any specific lock implementation.
type Store[T any] struct {
	filePath string
	locker   lock.Locker
}

// New creates a Store backed by filePath, using locker for mutual exclusion.
func New[T any](filePath string, locker lock.Locker) *Store[T] {
	return &Store[T]{filePath: filePath, locker: locker}
}

// Read deserializes the JSON file and passes the data to fn.
// The caller must already hold the lock (via TryLock).
func (s *Store[T]) Read(fn func(*T) error) error {
	var data T
	raw, err := os.ReadFile(s.filePath) //nolint:gosec // internal metadata
	if err != nil {
		if os.IsNotExist(err) {
			initData(&data)
			return fn(&data)
		}
		return fmt.Errorf("read %s: %w", s.filePath, err)
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("parse %s: %w", s.filePath, err)
	}
	initData(&data)
	return fn(&data)
}

// Write deserializes the JSON file, passes the data to fn, and atomically
// persists the result if fn returns nil.
// The caller must already hold the lock (via TryLock).
func (s *Store[T]) Write(fn func(*T) error) error {
	return s.Read(func(data *T) error {
		if err := fn(data); err != nil {
			return err
		}
		return utils.AtomicWriteJSON(s.filePath, data)
	})
}

// With acquires the lock (blocking), calls Read under lock, then releases.
func (s *Store[T]) With(ctx context.Context, fn func(*T) error) error {
	if err := s.locker.Lock(ctx); err != nil {
		return err
	}
	defer s.locker.Unlock(ctx) //nolint:errcheck
	return s.Read(fn)
}

// Update acquires the lock (blocking), calls Write under lock, then releases.
// If fn returns nil the data is atomically persisted.
func (s *Store[T]) Update(ctx context.Context, fn func(*T) error) error {
	if err := s.locker.Lock(ctx); err != nil {
		return err
	}
	defer s.locker.Unlock(ctx) //nolint:errcheck
	return s.Write(fn)
}

// TryLock delegates to the underlying locker.
func (s *Store[T]) TryLock(ctx context.Context) (bool, error) {
	return s.locker.TryLock(ctx)
}

// Unlock delegates to the underlying locker.
func (s *Store[T]) Unlock(ctx context.Context) error {
	return s.locker.Unlock(ctx)
}

func initData[T any](data *T) {
	if initer, ok := any(data).(storage.Initer); ok {
		initer.Init()
	}
}
