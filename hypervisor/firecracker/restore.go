package firecracker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// Restore stages a snapshot tar before replacing the running VM state.
func (fc *Firecracker) Restore(ctx context.Context, vmRef string, vmCfg *types.VMConfig, snapshot io.Reader) (*types.VM, error) {
	return fc.RestoreSequence(ctx, vmRef, hypervisor.RestoreSpec{
		VMCfg:         vmCfg,
		Snapshot:      snapshot,
		OverrideCheck: validateRestoreOverrides,
		Preflight:     fc.preflightRestore,
		Kill:          fc.killForRestore,
		// Lock every writable disk so recoverStaleBackup heals stale
		// data-*.raw.cocoon-clone-backup before restore overwrites them;
		// otherwise a future clone would rename the backup over restored data.
		Wrap: func(rec *hypervisor.VMRecord, inner func() error) error {
			return withSourceWritableDisksLocked(rec.StorageConfigs, inner)
		},
		BeforeMerge: func(rec *hypervisor.VMRecord) error {
			_ = os.Remove(filepath.Join(rec.RunDir, cowFileName))
			return nil
		},
		AfterExtract: func(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *hypervisor.VMRecord) (*types.VM, error) {
			return fc.restoreAfterExtract(ctx, vmID, vmCfg, rec, fc.conf.COWRawPath(vmID))
		},
	})
}

// killForRestore stops the running FC process and cleans up runtime files.
func (fc *Firecracker) killForRestore(ctx context.Context, vmID string, rec *hypervisor.VMRecord) error {
	return fc.KillForRestore(ctx, vmID, rec, func(pid int) error {
		sockPath := hypervisor.SocketPath(rec.RunDir)
		return fc.forceTerminate(ctx, sockPath, pid)
	}, runtimeFiles)
}

// restoreAfterExtract resumes from snapshot data already placed in runDir.
func (fc *Firecracker) restoreAfterExtract(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *hypervisor.VMRecord, cowPath string) (_ *types.VM, err error) {
	logger := log.WithFunc("firecracker.Restore")

	defer func() {
		if err != nil {
			fc.MarkError(ctx, vmID)
		}
	}()

	snapshotCOW := filepath.Join(rec.RunDir, cowFileName)
	if snapshotCOW != cowPath {
		if _, statErr := os.Stat(snapshotCOW); statErr == nil {
			if renameErr := os.Rename(snapshotCOW, cowPath); renameErr != nil {
				return nil, fmt.Errorf("move COW: %w", renameErr)
			}
		}
	}

	if vmCfg.Storage > 0 {
		if err = hypervisor.ExpandRawImage(cowPath, vmCfg.Storage); err != nil {
			return nil, fmt.Errorf("resize COW: %w", err)
		}
	}

	sockPath := hypervisor.SocketPath(rec.RunDir)

	withNetwork := len(rec.NetworkConfigs) > 0
	pid, launchErr := fc.launchProcess(ctx, rec, sockPath, withNetwork)
	if launchErr != nil {
		return nil, fmt.Errorf("launch FC: %w", launchErr)
	}

	defer func() {
		if err != nil {
			fc.AbortLaunch(ctx, pid, sockPath, rec.RunDir, runtimeFiles)
		}
	}()

	// Restore keeps the same VM, so drives and TAPs stay unchanged.
	if err = loadSnapshotFC(ctx, sockPath, rec.RunDir, nil); err != nil {
		return nil, fmt.Errorf("snapshot/load: %w", err)
	}

	hc := utils.NewSocketHTTPClient(sockPath)
	if err = resumeVM(ctx, hc); err != nil {
		return nil, fmt.Errorf("resume: %w", err)
	}

	logger.Infof(ctx, "VM %s restored from snapshot", vmID)
	return fc.FinalizeRestore(ctx, vmID, vmCfg, rec, pid)
}

// validateRestoreOverrides rejects CPU or memory changes FC cannot apply after load.
func validateRestoreOverrides(rec *hypervisor.VMRecord, vmCfg *types.VMConfig) error {
	if vmCfg.CPU != rec.Config.CPU {
		return fmt.Errorf("--cpu %d not supported: Firecracker cannot change CPU after snapshot/load (VM has %d)", vmCfg.CPU, rec.Config.CPU)
	}
	if vmCfg.Memory != rec.Config.Memory {
		return fmt.Errorf("--memory not supported: Firecracker cannot change memory after snapshot/load")
	}
	return nil
}
