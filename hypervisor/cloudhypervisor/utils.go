package cloudhypervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/projecteru2/core/log"

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

// savePayload writes the CH config payload to the per-VM run dir for debugging.
// Best-effort: errors are silently ignored.
func (ch *CloudHypervisor) savePayload(vmID string, cfg *chVMConfig) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(ch.conf.CHVMPayloadFile(vmID), data, 0o600) //nolint:gosec
}

// cleanupRuntimeFiles removes transient runtime files (socket, PID, vm.json)
// from a VM's run directory. Safe to call unconditionally.
func (ch *CloudHypervisor) cleanupRuntimeFiles(vmID string) {
	_ = os.Remove(ch.conf.CHVMSocketPath(vmID))
	_ = os.Remove(ch.conf.CHVMPIDFile(vmID))
	_ = os.Remove(ch.conf.CHVMPayloadFile(vmID))
}

// forEachVM runs fn for each ID, collects successes, and logs failures.
// When bestEffort is true, individual errors are logged and skipped.
// When bestEffort is false, the first error stops processing and is returned.
func forEachVM(ctx context.Context, ids []string, op string, bestEffort bool, fn func(context.Context, string) error) ([]string, error) {
	logger := log.WithFunc("cloudhypervisor." + op)
	var succeeded []string
	for _, id := range ids {
		if err := fn(ctx, id); err != nil {
			if !bestEffort {
				return succeeded, fmt.Errorf("%s VM %s: %w", op, id, err)
			}
			logger.Warnf(ctx, "%s VM %s: %v", op, id, err)
			continue
		}
		succeeded = append(succeeded, id)
	}
	return succeeded, nil
}
