package cloudhypervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

func (ch *CloudHypervisor) loadRecord(ctx context.Context, id string) (hypervisor.VMRecord, error) {
	var rec hypervisor.VMRecord
	return rec, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		var err error
		rec, err = utils.LookupCopy(idx.VMs, id)
		return err
	})
}

// verifyVMProcess checks that pid belongs to this specific VM's cloud-hypervisor
// process by matching the --api-socket path in /proc/{pid}/cmdline.
func (ch *CloudHypervisor) verifyVMProcess(pid int, vmID string) bool {
	return utils.VerifyProcessCmdline(pid, filepath.Base(ch.conf.CHBinary), ch.conf.CHVMSocketPath(vmID))
}

func (ch *CloudHypervisor) enrichRuntime(info *types.VMInfo) {
	info.SocketPath = ch.conf.CHVMSocketPath(info.ID)
	info.PID, _ = utils.ReadPIDFile(ch.conf.CHVMPIDFile(info.ID))
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
	_ = ch.updateState(ctx, id, types.VMStateError)
}

func (ch *CloudHypervisor) saveCmdline(vmID string, args []string) {
	line := ch.conf.CHBinary + " " + strings.Join(args, " ")
	_ = os.WriteFile(ch.conf.CHVMCmdlineFile(vmID), []byte(line), 0o600) //nolint:gosec
}

func (ch *CloudHypervisor) cleanupRuntimeFiles(vmID string) {
	_ = os.Remove(ch.conf.CHVMSocketPath(vmID))
	_ = os.Remove(ch.conf.CHVMPIDFile(vmID))
	_ = os.Remove(ch.conf.CHVMCmdlineFile(vmID))
}

func (ch *CloudHypervisor) removeVMDirs(ctx context.Context, vmID string) {
	logger := log.WithFunc("cloudhypervisor.removeVMDirs")
	if err := os.RemoveAll(ch.conf.CHVMRunDir(vmID)); err != nil {
		logger.Warnf(ctx, "run dir %s: %v", vmID, err)
	}
	if err := os.RemoveAll(ch.conf.CHVMLogDir(vmID)); err != nil {
		logger.Warnf(ctx, "log dir %s: %v", vmID, err)
	}
}
