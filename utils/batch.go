package utils

import (
	"context"
	"errors"
	"fmt"
)

// BatchResult holds the outcome of a best-effort batch operation.
type BatchResult struct {
	Succeeded []string
	Errors    []error
}

// Err returns the combined error from all failed operations.
func (r BatchResult) Err() error { return errors.Join(r.Errors...) }

// ForEach runs fn for each id, collecting successes and errors (best-effort).
// All ids are attempted regardless of individual failures.
func ForEach(ctx context.Context, ids []string, fn func(context.Context, string) error) BatchResult {
	var r BatchResult
	for _, id := range ids {
		if err := fn(ctx, id); err != nil {
			r.Errors = append(r.Errors, fmt.Errorf("%s: %w", id, err))
			continue
		}
		r.Succeeded = append(r.Succeeded, id)
	}
	return r
}
