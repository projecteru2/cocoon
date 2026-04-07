package firecracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// Restore reverts a running VM to a previous snapshot's state.
//
// The FC process is killed and restarted with the snapshot's memory and disk state.
// Network is preserved -- same netns, same tap, same MAC/IP.
// vmCfg carries the final resource config (already validated >= snapshot values).
func (fc *Firecracker) Restore(ctx context.Context, vmRef string, vmCfg *types.VMConfig, snapshot io.Reader) (*types.VM, error) {
	vmID, rec, cowPath, err := fc.prepareRestore(ctx, vmRef)
	if err != nil {
		return nil, err
	}

	_ = os.Remove(cowPath) // best-effort; extractTar overwrites

	if extractErr := utils.ExtractTar(rec.RunDir, snapshot); extractErr != nil {
		fc.markError(ctx, vmID)
		return nil, fmt.Errorf("extract snapshot: %w", extractErr)
	}

	return fc.restoreAfterExtract(ctx, vmID, vmCfg, rec, cowPath)
}

// prepareRestore handles the common setup for Restore and DirectRestore:
// resolve ref, load record, validate state, kill current FC, cleanup.
func (fc *Firecracker) prepareRestore(ctx context.Context, vmRef string) (string, *hypervisor.VMRecord, string, error) {
	vmID, err := fc.resolveRef(ctx, vmRef)
	if err != nil {
		return "", nil, "", err
	}

	rec, err := fc.loadRecord(ctx, vmID)
	if err != nil {
		return "", nil, "", err
	}

	if rec.State != types.VMStateRunning {
		return "", nil, "", fmt.Errorf("vm %s is %s, must be running to restore", vmID, rec.State)
	}

	sockPath := socketPath(rec.RunDir)
	killErr := fc.withRunningVM(ctx, &rec, func(pid int) error {
		return fc.forceTerminate(ctx, utils.NewSocketHTTPClient(sockPath), vmID, sockPath, pid)
	})
	if killErr != nil && !errors.Is(killErr, hypervisor.ErrNotRunning) {
		return "", nil, "", fmt.Errorf("stop running VM: %w", killErr)
	}
	cleanupRuntimeFiles(ctx, rec.RunDir)

	cowPath := fc.conf.COWRawPath(vmID)
	return vmID, &rec, cowPath, nil
}

// restoreAfterExtract contains all restore logic after snapshot data is in runDir.
// Shared by Restore (tar stream) and DirectRestore (direct file copy).
func (fc *Firecracker) restoreAfterExtract(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *hypervisor.VMRecord, cowPath string) (_ *types.VM, err error) {
	logger := log.WithFunc("firecracker.Restore")

	defer func() {
		if err != nil {
			fc.markError(ctx, vmID)
		}
	}()

	// Move extracted COW to canonical path if it was extracted into runDir.
	snapshotCOW := filepath.Join(rec.RunDir, "cow.raw")
	if snapshotCOW != cowPath {
		if _, statErr := os.Stat(snapshotCOW); statErr == nil {
			if renameErr := os.Rename(snapshotCOW, cowPath); renameErr != nil {
				return nil, fmt.Errorf("move COW: %w", renameErr)
			}
		}
	}

	if vmCfg.Storage > 0 {
		if err = expandRawImage(cowPath, vmCfg.Storage); err != nil {
			return nil, fmt.Errorf("resize COW: %w", err)
		}
	}

	sockPath := socketPath(rec.RunDir)

	withNetwork := len(rec.NetworkConfigs) > 0
	pid, launchErr := fc.launchProcess(ctx, rec, sockPath, withNetwork)
	if launchErr != nil {
		return nil, fmt.Errorf("launch FC: %w", launchErr)
	}

	defer func() {
		if err != nil {
			fc.abortLaunch(ctx, pid, sockPath, rec.RunDir)
		}
	}()

	hc := utils.NewSocketHTTPClient(sockPath)
	if err = loadSnapshotFC(ctx, hc, rec.RunDir); err != nil {
		return nil, fmt.Errorf("snapshot/load: %w", err)
	}

	// Re-configure drives after snapshot load.
	if err = fc.reconfigureDrives(ctx, hc, rec.StorageConfigs); err != nil {
		return nil, fmt.Errorf("reconfigure drives: %w", err)
	}

	if err = resumeVM(ctx, hc); err != nil {
		return nil, fmt.Errorf("resume: %w", err)
	}

	now := time.Now()
	if err = fc.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r := idx.VMs[vmID]
		if r == nil {
			return fmt.Errorf("vm %s disappeared from index", vmID)
		}
		r.Config = *vmCfg
		r.State = types.VMStateRunning
		r.StartedAt = &now
		r.UpdatedAt = now
		return nil
	}); err != nil {
		return nil, fmt.Errorf("update record: %w", err)
	}

	logger.Infof(ctx, "VM %s restored from snapshot", vmID)

	info := rec.VM
	info.Config = *vmCfg
	info.State = types.VMStateRunning
	info.PID = pid
	info.SocketPath = socketPath(rec.RunDir)
	info.StartedAt = &now
	info.UpdatedAt = now
	return &info, nil
}
