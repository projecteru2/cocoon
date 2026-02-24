package utils

import "fmt"

// LookupCopy returns a copy of the value at key in m.
// Returns an error if the key is absent or the stored pointer is nil.
// The caller receives a detached value, safe to use after any lock is released.
func LookupCopy[T any](m map[string]*T, key string) (T, error) {
	v := m[key]
	if v == nil {
		var zero T
		return zero, fmt.Errorf("%q not found", key)
	}
	return *v, nil
}
