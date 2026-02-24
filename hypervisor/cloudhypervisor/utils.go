package cloudhypervisor

import (
	"context"
	"time"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// loadRecord reads a copy of a VM record from the index under lock.
// The returned record is detached from the index and safe to use after the lock is released.
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
// Sets StartedAt/StoppedAt timestamps for the corresponding terminal states.
func (ch *CloudHypervisor) updateState(ctx context.Context, id string, state types.VMState) error {
	now := time.Now()
	return ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r := idx.VMs[id]
		if r == nil {
			return nil
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
