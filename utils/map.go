package utils

import (
	"fmt"
	"maps"
)

// LookupCopy returns a shallow copy of the value at key in m.
// Returns an error if the key is absent or the stored pointer is nil.
// NOTE: this is a shallow copy — pointer, slice, and map fields inside T
// still reference the original data. Callers must not mutate such fields
// on the returned value without additional deep-copy logic.
func LookupCopy[T any](m map[string]*T, key string) (T, error) {
	v := m[key]
	if v == nil {
		var zero T
		return zero, fmt.Errorf("%q not found", key)
	}
	return *v, nil
}

// MergeSets unions any number of set maps into a new set.
func MergeSets[K comparable](sets ...map[K]struct{}) map[K]struct{} {
	total := 0
	for _, s := range sets {
		total += len(s)
	}
	out := make(map[K]struct{}, total)
	for _, s := range sets {
		maps.Copy(out, s)
	}
	return out
}

// MapValues projects every non-nil value in m through fn into a slice.
// Used by List endpoints to materialize record maps without hand-rolled loops.
func MapValues[K comparable, V, R any](m map[K]*V, fn func(*V) R) []R {
	if len(m) == 0 {
		return nil
	}
	out := make([]R, 0, len(m))
	for _, v := range m {
		if v == nil {
			continue
		}
		out = append(out, fn(v))
	}
	return out
}
