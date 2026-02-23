package json

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/lock/flock"
	"github.com/projecteru2/cocoon/storage"
	"github.com/projecteru2/cocoon/utils"
)

// Store provides flock-protected read/modify/write access to a JSON file.
// T is the top-level structure stored in the file (must have exported fields with json tags).
// If *T implements storage.Initer, Init() is called automatically after loading.
type Store[T any] struct {
	lockPath string
	filePath string
}

// New creates a Store for the given lock and data file paths.
func New[T any](lockPath, filePath string) *Store[T] {
	return &Store[T]{lockPath: lockPath, filePath: filePath}
}

// With loads the JSON file under flock and passes the deserialized data to fn.
// If the file does not exist, fn receives a zero-value T.
// If *T implements storage.Initer, Init() is called before fn (handles nil maps, etc.).
// The lock is held for the duration of fn.
func (s *Store[T]) With(ctx context.Context, fn func(*T) error) error {
	return lock.WithLock(ctx, flock.New(s.lockPath), func() error {
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
	})
}

// Update performs a read-modify-write on the JSON file under flock.
// If fn returns nil the data is atomically written back.
func (s *Store[T]) Update(ctx context.Context, fn func(*T) error) error {
	return s.With(ctx, func(data *T) error {
		if err := fn(data); err != nil {
			return err
		}
		return utils.AtomicWriteJSON(s.filePath, data)
	})
}

func initData[T any](data *T) {
	if initer, ok := any(data).(storage.Initer); ok {
		initer.Init()
	}
}
