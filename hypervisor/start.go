package hypervisor

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/utils"
)

// StartAll runs startOne for each ref and batch-flips the succeeded set
// to Running so a partial batch doesn't leave half-Running state.
func (b *Backend) StartAll(ctx context.Context, refs []string, startOne func(context.Context, string) error) ([]string, error) {
	ids, err := b.ResolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	succeeded, forEachErr := b.ForEachVM(ctx, ids, "Start", startOne)
	if batchErr := b.BatchMarkStarted(ctx, succeeded); batchErr != nil {
		log.WithFunc(b.Typ+".Start").Warnf(ctx, "batch state update: %v", batchErr)
	}
	return succeeded, forEachErr
}

// PrepareStart loads the record, verifies not-running, ensures dirs exist.
func (b *Backend) PrepareStart(ctx context.Context, id string, runtimeFiles []string) (*VMRecord, error) {
	rec, err := b.LoadRecord(ctx, id)
	if err != nil {
		return nil, err
	}

	runErr := b.WithRunningVM(ctx, &rec, func(_ int) error { return nil })
	switch {
	case runErr == nil:
		return nil, nil // already running
	case errors.Is(runErr, ErrNotRunning):
		// expected — proceed to start
	default:
		return nil, fmt.Errorf("reconcile running VM %s: %w", id, runErr)
	}

	if err = utils.EnsureDirs(rec.RunDir, rec.LogDir); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}
	CleanupRuntimeFiles(ctx, rec.RunDir, runtimeFiles)
	return &rec, nil
}

// LaunchVMProcess starts spec.Cmd and waits for the API socket. On any error
// after Start, the process is killed and the PID file is removed. Caller
// reaps cmd via cmd.Wait() in a goroutine on success.
func (b *Backend) LaunchVMProcess(ctx context.Context, spec LaunchSpec) (pid int, err error) {
	started := false
	pidWritten := false
	binaryName := b.Conf.BinaryName()
	defer func() {
		if err == nil {
			return
		}
		if started {
			_ = spec.Cmd.Process.Kill()
			_ = spec.Cmd.Wait()
		}
		if pidWritten {
			_ = os.Remove(spec.PIDPath)
		}
		if spec.OnFail != nil {
			spec.OnFail()
		}
	}()

	if spec.NetnsPath != "" {
		restore, nsErr := EnterNetns(spec.NetnsPath)
		if nsErr != nil {
			return 0, fmt.Errorf("enter netns: %w", nsErr)
		}
		defer restore()
	}

	if err = spec.Cmd.Start(); err != nil {
		return 0, fmt.Errorf("exec %s: %w", binaryName, err)
	}
	started = true
	pid = spec.Cmd.Process.Pid

	if err = utils.WritePIDFile(spec.PIDPath, pid); err != nil {
		return 0, fmt.Errorf("write PID file: %w", err)
	}
	pidWritten = true

	if err = WaitForSocket(ctx, spec.SockPath, pid, b.Conf.SocketWaitTimeout(), binaryName); err != nil {
		return 0, err
	}
	return pid, nil
}

// AbortLaunch terminates a failed launch and clears runtime files.
func (b *Backend) AbortLaunch(ctx context.Context, pid int, sockPath, runDir string, runtimeFiles []string) {
	_ = utils.TerminateProcess(ctx, pid, b.Conf.BinaryName(), sockPath, b.Conf.TerminateGracePeriod())
	CleanupRuntimeFiles(ctx, runDir, runtimeFiles)
}
