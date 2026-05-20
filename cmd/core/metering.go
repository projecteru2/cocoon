package core

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/metering"
)

const (
	meteringSubdir = "metering"
	meteringFile   = "ledger.jsonl"
)

var (
	meteringOnce sync.Once
	meteringRec  metering.Recorder
)

// MeteringRecorder returns a process-wide lifecycle recorder; lazy-init shares one ledger fd across all backends, falls back to NopRecorder on fs error.
func MeteringRecorder(ctx context.Context, conf *config.Config) metering.Recorder {
	meteringOnce.Do(func() {
		dir := filepath.Join(conf.RootDir, meteringSubdir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			log.WithFunc("core.MeteringRecorder").Warnf(ctx, "mkdir %s: %v; metering disabled", dir, err)
			meteringRec = metering.NopRecorder{}
			return
		}
		meteringRec = metering.NewFileRecorder(ctx, filepath.Join(dir, meteringFile))
	})
	return meteringRec
}
