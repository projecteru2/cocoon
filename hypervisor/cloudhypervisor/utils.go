package cloudhypervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

func (ch *CloudHypervisor) loadRecord(ctx context.Context, id string) (hypervisor.VMRecord, error) {
	var rec hypervisor.VMRecord
	return rec, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		var err error
		rec, err = utils.LookupCopy(idx.VMs, id)
		return err
	})
}

func (ch *CloudHypervisor) chBinaryName() string {
	return filepath.Base(ch.conf.CHBinary)
}

func (ch *CloudHypervisor) withRunningVM(ctx context.Context, rec *hypervisor.VMRecord, fn func(pid int) error) error {
	pid, pidErr := utils.ReadPIDFile(pidFile(rec.RunDir))
	if pidErr != nil && !os.IsNotExist(pidErr) {
		log.WithFunc("cloudhypervisor.withRunningVM").Warnf(ctx, "read PID file: %v", pidErr)
	}
	if !utils.VerifyProcessCmdline(pid, ch.chBinaryName(), socketPath(rec.RunDir)) {
		return hypervisor.ErrNotRunning
	}
	return fn(pid)
}

func (ch *CloudHypervisor) updateState(ctx context.Context, id string, state types.VMState) error {
	now := time.Now()
	return ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r := idx.VMs[id]
		if r == nil {
			return fmt.Errorf("VM %q not found in index", id)
		}
		r.State = state
		r.UpdatedAt = now
		switch state {
		case types.VMStateRunning:
			r.StartedAt = &now
		case types.VMStateStopped:
			r.StoppedAt = &now
		}
		return nil
	})
}

func (ch *CloudHypervisor) markError(ctx context.Context, id string) {
	if err := ch.updateState(ctx, id, types.VMStateError); err != nil {
		log.WithFunc("cloudhypervisor.markError").Warnf(ctx, "mark VM %s error: %v", id, err)
	}
}

func (ch *CloudHypervisor) saveCmdline(ctx context.Context, rec *hypervisor.VMRecord, args []string) {
	line := ch.conf.CHBinary + " " + strings.Join(args, " ")
	if err := os.WriteFile(filepath.Join(rec.RunDir, "cmdline"), []byte(line), 0o600); err != nil {
		log.WithFunc("cloudhypervisor.saveCmdline").Warnf(ctx, "save cmdline: %v", err)
	}
}

// reserveVM writes a placeholder VMRecord (state=Creating) so that GC won't
// treat the VM's directories as orphans. Used by both Create and Clone.
func (ch *CloudHypervisor) reserveVM(ctx context.Context, id string, vmCfg *types.VMConfig, blobIDs map[string]struct{}, runDir, logDir string) error {
	now := time.Now()
	return ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		if idx.VMs[id] != nil {
			return fmt.Errorf("ID collision %q (retry)", id)
		}
		if dup, ok := idx.Names[vmCfg.Name]; ok {
			return fmt.Errorf("VM name %q already exists (id: %s)", vmCfg.Name, dup)
		}
		idx.VMs[id] = &hypervisor.VMRecord{
			VM: types.VM{
				ID: id, State: types.VMStateCreating,
				Config: *vmCfg, CreatedAt: now, UpdatedAt: now,
			},
			ImageBlobIDs: blobIDs,
			RunDir:       runDir,
			LogDir:       logDir,
		}
		idx.Names[vmCfg.Name] = id
		return nil
	})
}

// rollbackCreate removes a placeholder VM record from the DB.
func (ch *CloudHypervisor) rollbackCreate(ctx context.Context, id, name string) {
	if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		delete(idx.VMs, id)
		if name != "" && idx.Names[name] == id {
			delete(idx.Names, name)
		}
		return nil
	}); err != nil {
		log.WithFunc("cloudhypervisor.rollbackCreate").Warnf(ctx, "rollback VM %s (name=%s): %v", id, name, err)
	}
}

// abortLaunch kills a CH process and removes runtime files after a failed launch sequence.
func (ch *CloudHypervisor) abortLaunch(ctx context.Context, pid int, sockPath, runDir string) {
	_ = utils.TerminateProcess(ctx, pid, ch.chBinaryName(), sockPath, ch.conf.TerminateGracePeriod())
	cleanupRuntimeFiles(ctx, runDir)
}

// cowPath returns the writable COW disk path for a VM.
// Direct-boot (OCI) uses a raw file; UEFI (cloudimg) uses a qcow2 overlay.
func (ch *CloudHypervisor) cowPath(vmID string, directBoot bool) string {
	if directBoot {
		return ch.conf.COWRawPath(vmID)
	}
	return ch.conf.OverlayPath(vmID)
}
