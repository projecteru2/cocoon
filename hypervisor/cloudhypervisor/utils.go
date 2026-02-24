package cloudhypervisor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// loadRecord reads a VM record from the index under lock.
// Returns a value copy of VMRecord — callers must treat pointer fields as read-only.
func (ch *CloudHypervisor) loadRecord(ctx context.Context, id string) (hypervisor.VMRecord, error) {
	var rec hypervisor.VMRecord
	return rec, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		var err error
		rec, err = utils.LookupCopy(idx.VMs, id)
		return err
	})
}

// enrichRuntime populates the runtime-only fields of info from live sources:
//   - SocketPath is always derived from config (deterministic).
//   - PID is read from the PID file; 0 means the VM is not running.
func (ch *CloudHypervisor) enrichRuntime(info *types.VMInfo) {
	info.SocketPath = ch.conf.CHVMSocketPath(info.ID)
	info.PID, _ = utils.ReadPIDFile(ch.conf.CHVMPIDFile(info.ID))
}

// updateState atomically transitions a VM to a new state in the DB.
// Returns an error if the VM no longer exists in the index.
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

// markError transitions a VM to the Error state. Best-effort: errors are ignored
// because the caller already has a primary error to return.
func (ch *CloudHypervisor) markError(ctx context.Context, id string) {
	_ = ch.updateState(ctx, id, types.VMStateError)
}

// saveCmdline writes the full cloud-hypervisor launch command to the per-VM
// run dir for debugging. Best-effort: errors are silently ignored.
func (ch *CloudHypervisor) saveCmdline(vmID string, args []string) {
	line := ch.conf.CHBinary + " " + strings.Join(args, " ")
	_ = os.WriteFile(ch.conf.CHVMCmdlineFile(vmID), []byte(line), 0o600) //nolint:gosec
}

// cleanupRuntimeFiles removes transient runtime files (socket, PID, cmdline)
// from a VM's run directory. Used by start/stop to clean stale state without
// touching overlays or logs. Safe to call unconditionally.
func (ch *CloudHypervisor) cleanupRuntimeFiles(vmID string) {
	_ = os.Remove(ch.conf.CHVMSocketPath(vmID))
	_ = os.Remove(ch.conf.CHVMPIDFile(vmID))
	_ = os.Remove(ch.conf.CHVMCmdlineFile(vmID))
}

// removeVMDirs removes the VM's entire run and log directories, including
// large files like overlay.qcow2, cow.raw, serial.log, etc.
// Used by Delete for complete cleanup.
func (ch *CloudHypervisor) removeVMDirs(vmID string) {
	_ = os.RemoveAll(ch.conf.CHVMRunDir(vmID))
	_ = os.RemoveAll(ch.conf.CHVMLogDir(vmID))
}
