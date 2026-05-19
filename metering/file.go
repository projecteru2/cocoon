package metering

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"github.com/projecteru2/core/log"
)

// POSIX guarantees a single write(2) to an O_APPEND file is atomic relative
// to other writes from any process; concatenating JSON+newline into one Write
// keeps the ledger valid even when multiple cocoon CLI processes append
// concurrently. (Mutex below only serializes the writes from a single process.)

// FileRecorder appends JSON-encoded entries (one per line) to a file under sync.Mutex.
type FileRecorder struct {
	mu sync.Mutex
	f  *os.File
}

// NewFileRecorder opens path append-only; on open failure logs a warning and returns NopRecorder so callers never see nil.
func NewFileRecorder(ctx context.Context, path string) Recorder {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // internal runtime path
	if err != nil {
		log.WithFunc("metering.NewFileRecorder").Warnf(ctx, "open %s: %v; metering disabled", path, err)
		return NopRecorder{}
	}
	return &FileRecorder{f: f}
}

// Emit marshals e and appends one line atomically; write errors are logged and swallowed so the caller's state machine is never blocked.
func (r *FileRecorder) Emit(ctx context.Context, e Entry) {
	data, err := json.Marshal(e)
	if err != nil {
		log.WithFunc("metering.FileRecorder.Emit").Warnf(ctx, "marshal entry: %v", err)
		return
	}
	data = append(data, '\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.f.Write(data); err != nil {
		log.WithFunc("metering.FileRecorder.Emit").Warnf(ctx, "write entry: %v", err)
	}
}
