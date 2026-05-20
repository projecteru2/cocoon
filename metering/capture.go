package metering

import (
	"context"
	"sync"
)

// CaptureRecorder accumulates entries in memory; intended for tests that assert emit sequences.
type CaptureRecorder struct {
	mu      sync.Mutex
	entries []Entry
}

func (r *CaptureRecorder) Emit(_ context.Context, e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

// Entries returns a snapshot copy so callers can mutate freely.
func (r *CaptureRecorder) Entries() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}
