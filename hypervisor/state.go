package hypervisor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// WithRunningVM calls fn if rec still points to a live VM process.
func (b *Backend) WithRunningVM(ctx context.Context, rec *VMRecord, fn func(pid int) error) error {
	pid, pidErr := utils.ReadPIDFile(b.PIDFilePath(rec.RunDir))
	if pidErr != nil && !errors.Is(pidErr, fs.ErrNotExist) {
		log.WithFunc(b.Typ+".WithRunningVM").Warnf(ctx, "read PID file: %v", pidErr)
	}
	if !utils.VerifyProcessCmdline(pid, b.Conf.BinaryName(), SocketPath(rec.RunDir)) {
		return ErrNotRunning
	}
	return fn(pid)
}

// WithPausedVM pauses, runs fn, resumes. Resume on the success path is eager
// so its error promotes to the return; on fn-error the deferred resume only
// logs (the inner error wins).
func (b *Backend) WithPausedVM(ctx context.Context, rec *VMRecord, pause, resume, fn func() error) error {
	return b.WithRunningVM(ctx, rec, func(_ int) error {
		if err := pause(); err != nil {
			return fmt.Errorf("pause: %w", err)
		}
		var resumed bool
		var resumeErr error
		logger := log.WithFunc(b.Typ + ".WithPausedVM")
		doResume := func() {
			if resumed {
				return
			}
			resumed = true
			resumeErr = resume()
			if resumeErr != nil {
				logger.Warnf(ctx, "resume VM %s: %v", rec.ID, resumeErr)
			}
		}
		defer doResume()
		if err := fn(); err != nil {
			return err
		}
		doResume()
		if resumeErr != nil {
			return fmt.Errorf("snapshot data captured but resume failed: %w", resumeErr)
		}
		return nil
	})
}

// UpdateStates batch-updates the State field for ids; sets StartedAt/StoppedAt as appropriate.
func (b *Backend) UpdateStates(ctx context.Context, ids []string, state types.VMState) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		for _, id := range ids {
			r := idx.VMs[id]
			if r == nil {
				continue
			}
			r.State = state
			r.UpdatedAt = now
			switch state {
			case types.VMStateRunning:
				r.StartedAt = &now
			case types.VMStateStopped:
				r.StoppedAt = &now
			}
		}
		return nil
	})
}

// MarkError flips a single VM's state to VMStateError, logging on persist failure.
func (b *Backend) MarkError(ctx context.Context, id string) {
	if err := b.UpdateStates(ctx, []string{id}, types.VMStateError); err != nil {
		log.WithFunc(b.Typ+".MarkError").Errorf(ctx, err, "mark VM %s error", id)
	}
}

// BatchMarkStarted flips ids to VMStateRunning and stamps FirstBooted=true in one DB write.
func (b *Backend) BatchMarkStarted(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		for _, id := range ids {
			r := idx.VMs[id]
			if r == nil {
				continue
			}
			r.State = types.VMStateRunning
			r.StartedAt = &now
			r.UpdatedAt = now
			r.FirstBooted = true
		}
		return nil
	})
}

// CleanStalePlaceholders removes "creating" records past GC grace period.
func (b *Backend) CleanStalePlaceholders(_ context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-CreatingStateGCGrace)
	return b.DB.WriteRaw(func(idx *VMIndex) error {
		utils.CleanStaleRecords(idx.VMs, idx.Names, ids,
			func(r *VMRecord) string { return r.Config.Name },
			func(r *VMRecord) bool {
				return r.State == types.VMStateCreating && r.UpdatedAt.Before(cutoff)
			},
		)
		return nil
	})
}
