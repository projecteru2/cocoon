package hypervisor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/metering"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

const socketProbeTimeout = 2 * time.Second

// WithRunningVM calls fn if rec still points to a live VM process.
func (b *Backend) WithRunningVM(ctx context.Context, rec *VMRecord, fn func(pid int) error) error {
	logger := log.WithFunc(b.Typ + ".WithRunningVM")
	pid, pidErr := utils.ReadPIDFile(b.PIDFilePath(rec.RunDir))
	if pidErr != nil && !errors.Is(pidErr, fs.ErrNotExist) {
		logger.Warnf(ctx, "read PID file: %v", pidErr)
	}
	sockPath := SocketPath(rec.RunDir)
	if utils.VerifyProcessCmdline(pid, b.Conf.BinaryName(), sockPath) {
		return fn(pid)
	}
	// Covers pidfile/socket cleaned up before VMM exited. Fail-closed if scan errors so callers don't treat inconclusive state as ErrNotRunning.
	scanned, scanErr := utils.FindVMMByCmdline(b.Conf.BinaryName(), sockPath)
	if scanErr != nil {
		return fmt.Errorf("vm %s: pidfile-based check failed and /proc scan errored: %w (resolve the host issue and retry)", rec.ID, scanErr)
	}
	if len(scanned) == 0 {
		return ErrNotRunning
	}
	logger.Warnf(ctx, "VM %s recovered live pids %v via cmdline scan", rec.ID, scanned)
	return fn(scanned[0])
}

// IsAPISocketLive: (true,nil)=confirmed live; (false,nil)=ENOENT/ECONNREFUSED; (true,err)=fail-closed for unknown dial errors.
func (b *Backend) IsAPISocketLive(ctx context.Context, rec *VMRecord) (bool, error) {
	sock := SocketPath(rec.RunDir)
	dialCtx, cancel := context.WithTimeout(ctx, socketProbeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", sock)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return false, nil
	}
	return true, err
}

// WithPausedVM pauses, runs fn, resumes; eager resume on success promotes its error, deferred resume on fn-error only logs.
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

// UpdateStates batch-updates State + StartedAt/StoppedAt; only Running→Stopped emits compute.stop (Error paths can't prove the process is dead).
func (b *Backend) UpdateStates(ctx context.Context, ids []string, state types.VMState) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	var stopped []metering.Entry
	if err := b.DB.Update(ctx, func(idx *VMIndex) error {
		for _, id := range ids {
			r := idx.VMs[id]
			if r == nil {
				continue
			}
			oldState := r.State
			r.State = state
			r.UpdatedAt = now
			switch state {
			case types.VMStateRunning:
				r.StartedAt = &now
			case types.VMStateStopped:
				r.StoppedAt = &now
				if oldState == types.VMStateRunning {
					stopped = append(stopped, b.makeEntry(metering.KindVMComputeStop, id, metering.ReasonStopUser, shapeFromConfig(r.Config), now))
				}
			case types.VMStateError:
				// Don't write StoppedAt — many MarkError paths can't prove the process is dead, so the compute interval stays open in the ledger until a confirmed-dead path (closeStaleComputeInterval / DeleteAll) closes it.
			}
		}
		return nil
	}); err != nil {
		return err
	}
	b.emitAll(ctx, stopped)
	return nil
}

// MarkError flips a single VM's state to VMStateError, logging on persist failure.
func (b *Backend) MarkError(ctx context.Context, id string) {
	if err := b.UpdateStates(ctx, []string{id}, types.VMStateError); err != nil {
		log.WithFunc(b.Typ+".MarkError").Errorf(ctx, err, "mark VM %s error", id)
	}
}

// BatchMarkStarted flips ids to VMStateRunning; State==Running entrants are stale-running (close stop-crash, then open fresh).
func (b *Backend) BatchMarkStarted(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	var emits []metering.Entry
	if err := b.DB.Update(ctx, func(idx *VMIndex) error {
		for _, id := range ids {
			r := idx.VMs[id]
			if r == nil {
				continue
			}
			shape := shapeFromConfig(r.Config)
			if hasOpenComputeInterval(r) {
				emits = append(emits, b.makeEntry(metering.KindVMComputeStop, id, metering.ReasonStopCrash, shape, now))
				r.StoppedAt = &now
			}
			reason := metering.ReasonBoot
			if r.FirstBooted {
				reason = metering.ReasonRestart
			}
			emits = append(emits, b.makeEntry(metering.KindVMComputeStart, id, reason, shape, now))
			r.State = types.VMStateRunning
			r.StartedAt = &now
			r.UpdatedAt = now
			r.FirstBooted = true
		}
		return nil
	}); err != nil {
		return err
	}
	b.emitAll(ctx, emits)
	return nil
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

// closeStaleComputeInterval emits stop-crash and writes StoppedAt; precondition: caller confirmed the process is dead.
func (b *Backend) closeStaleComputeInterval(ctx context.Context, rec *VMRecord) {
	now := time.Now()
	if err := b.DB.Update(ctx, func(idx *VMIndex) error {
		r := idx.VMs[rec.ID]
		if r == nil || !hasOpenComputeInterval(r) {
			return nil
		}
		if r.State == types.VMStateRunning {
			r.State = types.VMStateStopped
		}
		r.StoppedAt = &now
		r.UpdatedAt = now
		return nil
	}); err != nil {
		log.WithFunc(b.Typ+".closeStaleComputeInterval").Warnf(ctx, "close interval for %s: %v", rec.ID, err)
		return
	}
	b.Metering.Emit(ctx, b.makeEntry(metering.KindVMComputeStop, rec.ID, metering.ReasonStopCrash, shapeFromConfig(rec.Config), now))
}

// hasOpenComputeInterval reports whether the VM still has an unclosed compute interval in the ledger.
func hasOpenComputeInterval(r *VMRecord) bool {
	if r == nil || r.StartedAt == nil {
		return false
	}
	return r.StoppedAt == nil || r.StartedAt.After(*r.StoppedAt)
}
