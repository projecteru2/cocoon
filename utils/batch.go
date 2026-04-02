package utils

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"
)

// BatchResult holds the outcome of a best-effort batch operation.
type BatchResult struct {
	Succeeded []string
	Errors    []error
}

// Err returns the combined error from all failed operations.
func (r BatchResult) Err() error { return errors.Join(r.Errors...) }

// ForEach runs fn for each id concurrently, collecting successes and errors
// (best-effort). All ids are attempted regardless of individual failures.
// An optional concurrency limit caps in-flight goroutines; zero or omitted
// means no limit.
func ForEach(ctx context.Context, ids []string, fn func(context.Context, string) error, concurrency ...int) BatchResult {
	if len(ids) == 0 {
		return BatchResult{}
	}

	limit := 0
	if len(concurrency) > 0 {
		limit = concurrency[0]
	}

	type result struct {
		id  string
		err error
	}

	results := make([]result, len(ids))
	g, gctx := errgroup.WithContext(ctx)
	if limit > 0 {
		g.SetLimit(limit)
	}

	for i, id := range ids {
		g.Go(func() error {
			results[i] = result{id: id, err: fn(gctx, id)}
			return nil // always nil so errgroup processes all ids (best-effort)
		})
	}
	_ = g.Wait()

	var r BatchResult
	for _, res := range results {
		if res.err != nil {
			r.Errors = append(r.Errors, fmt.Errorf("%s: %w", res.id, res.err))
		} else {
			r.Succeeded = append(r.Succeeded, res.id)
		}
	}
	return r
}
