package hypervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// KillForRestore stops the running VM via the backend-specific terminate hook
// and clears runtime files.
func (b *Backend) KillForRestore(ctx context.Context, vmID string, rec *VMRecord, terminate func(pid int) error, runtimeFiles []string) error {
	killErr := b.WithRunningVM(ctx, rec, terminate)
	if killErr != nil && !errors.Is(killErr, ErrNotRunning) {
		b.MarkError(ctx, vmID)
		return fmt.Errorf("stop running VM: %w", killErr)
	}
	CleanupRuntimeFiles(ctx, rec.RunDir, runtimeFiles)
	return nil
}

// ResolveForRestore resolves vmRef and validates the VM is running.
func (b *Backend) ResolveForRestore(ctx context.Context, vmRef string) (string, *VMRecord, error) {
	vmID, err := b.ResolveRef(ctx, vmRef)
	if err != nil {
		return "", nil, err
	}
	rec, err := b.LoadRecord(ctx, vmID)
	if err != nil {
		return "", nil, err
	}
	if rec.State != types.VMStateRunning {
		return "", nil, fmt.Errorf("vm %s is %s, must be running to restore", vmID, rec.State)
	}
	return vmID, &rec, nil
}

// FinalizeRestore updates DB and assembles the returned VM after restore.
func (b *Backend) FinalizeRestore(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *VMRecord, pid int) (*types.VM, error) {
	now := time.Now()
	if err := b.DB.Update(ctx, func(idx *VMIndex) error {
		r, err := idx.GetRecord(vmID)
		if err != nil {
			return err
		}
		r.Config = *vmCfg
		r.State = types.VMStateRunning
		r.StartedAt = &now
		r.UpdatedAt = now
		return nil
	}); err != nil {
		return nil, fmt.Errorf("update record: %w", err)
	}

	info := rec.VM
	info.Config = *vmCfg
	info.State = types.VMStateRunning
	info.PID = pid
	info.SocketPath = SocketPath(rec.RunDir)
	info.StartedAt = &now
	info.UpdatedAt = now
	return &info, nil
}

// RestoreSequence is the shared restore skeleton. Staging happens before
// the kill so a preflight failure leaves the original VM running.
func (b *Backend) RestoreSequence(ctx context.Context, vmRef string, spec RestoreSpec) (*types.VM, error) {
	if err := ValidateHostCPU(spec.VMCfg.CPU); err != nil {
		return nil, err
	}
	vmID, rec, err := b.ResolveForRestore(ctx, vmRef)
	if err != nil {
		return nil, err
	}

	stagingDir, cleanupStaging, err := PrepareStagingDir(rec.RunDir, spec.Snapshot)
	if err != nil {
		return nil, err
	}
	defer cleanupStaging()

	if preflightErr := spec.Preflight(stagingDir, rec); preflightErr != nil {
		return nil, fmt.Errorf("snapshot preflight: %w", preflightErr)
	}
	if killErr := spec.Kill(ctx, vmID, rec); killErr != nil {
		return nil, killErr
	}

	var result *types.VM
	inner := func() error {
		if spec.BeforeMerge != nil {
			if err := spec.BeforeMerge(rec); err != nil {
				return err
			}
		}
		if mergeErr := MergeDirInto(stagingDir, rec.RunDir); mergeErr != nil {
			b.MarkError(ctx, vmID)
			return fmt.Errorf("apply staged snapshot: %w", mergeErr)
		}
		var afterErr error
		result, afterErr = spec.AfterExtract(ctx, vmID, spec.VMCfg, rec)
		return afterErr
	}
	if spec.Wrap != nil {
		if err := spec.Wrap(rec, inner); err != nil {
			return nil, err
		}
	} else if err := inner(); err != nil {
		return nil, err
	}
	return result, nil
}

// DirectRestoreSequence restores from a local snapshot directory; Populate
// replaces the tar staging+merge step used by RestoreSequence.
func (b *Backend) DirectRestoreSequence(ctx context.Context, vmRef string, spec DirectRestoreSpec) (*types.VM, error) {
	if err := ValidateHostCPU(spec.VMCfg.CPU); err != nil {
		return nil, err
	}
	vmID, rec, err := b.ResolveForRestore(ctx, vmRef)
	if err != nil {
		return nil, err
	}

	if preflightErr := spec.Preflight(spec.SrcDir, rec); preflightErr != nil {
		return nil, fmt.Errorf("snapshot preflight: %w", preflightErr)
	}
	if killErr := spec.Kill(ctx, vmID, rec); killErr != nil {
		return nil, killErr
	}

	var result *types.VM
	inner := func() error {
		if populateErr := spec.Populate(rec, spec.SrcDir); populateErr != nil {
			b.MarkError(ctx, vmID)
			return populateErr
		}
		var afterErr error
		result, afterErr = spec.AfterExtract(ctx, vmID, spec.VMCfg, rec)
		return afterErr
	}
	if spec.Wrap != nil {
		if wrapErr := spec.Wrap(rec, inner); wrapErr != nil {
			return nil, wrapErr
		}
	} else if innerErr := inner(); innerErr != nil {
		return nil, innerErr
	}
	return result, nil
}

// PrepareStagingDir extracts the snapshot tar into a sibling staging dir.
func PrepareStagingDir(runDir string, snapshot io.Reader) (stagingDir string, cleanup func(), err error) {
	stagingDir = runDir + ".restore-staging"
	if err = os.RemoveAll(stagingDir); err != nil {
		return "", nil, fmt.Errorf("clear staging dir: %w", err)
	}
	if err = os.MkdirAll(stagingDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create staging dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(stagingDir) } //nolint:errcheck,gosec
	if err = utils.ExtractTar(stagingDir, snapshot); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extract snapshot: %w", err)
	}
	return stagingDir, cleanup, nil
}
