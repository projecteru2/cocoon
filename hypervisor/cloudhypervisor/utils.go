package cloudhypervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

func (ch *CloudHypervisor) chBinaryName() string {
	return filepath.Base(ch.conf.CHBinary)
}

func (ch *CloudHypervisor) withRunningVM(id string, fn func(pid int) error) error {
	pid, _ := utils.ReadPIDFile(ch.conf.CHVMPIDFile(id))
	if !utils.VerifyProcessCmdline(pid, ch.chBinaryName(), ch.conf.CHVMSocketPath(id)) {
		return hypervisor.ErrNotRunning
	}
	return fn(pid)
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
	_ = os.Remove(ch.conf.CHVMConsoleSock(vmID))
}

func (ch *CloudHypervisor) removeVMDirs(_ context.Context, vmID string) error {
	var errs []error
	if err := os.RemoveAll(ch.conf.CHVMRunDir(vmID)); err != nil {
		errs = append(errs, fmt.Errorf("run dir %s: %w", vmID, err))
	}
	if err := os.RemoveAll(ch.conf.CHVMLogDir(vmID)); err != nil {
		errs = append(errs, fmt.Errorf("log dir %s: %w", vmID, err))
	}
	return errors.Join(errs...)
}

// rollbackCreate removes a placeholder VM record from the DB.
func (ch *CloudHypervisor) rollbackCreate(ctx context.Context, id, name string) {
	_ = ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		delete(idx.VMs, id)
		delete(idx.Names, name)
		return nil
	})
}
