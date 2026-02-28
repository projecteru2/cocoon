package cloudhypervisor

import (
	"context"
	"errors"
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

func (ch *CloudHypervisor) chBinaryName() string {
	return filepath.Base(ch.conf.CHBinary)
}

// socketPath returns the API socket path under a VM's run directory.
func socketPath(runDir string) string { return filepath.Join(runDir, "api.sock") }

// pidFile returns the PID file path under a VM's run directory.
func pidFile(runDir string) string { return filepath.Join(runDir, "ch.pid") }

func (ch *CloudHypervisor) withRunningVM(rec *hypervisor.VMRecord, fn func(pid int) error) error {
	pid, _ := utils.ReadPIDFile(pidFile(rec.RunDir))
	if !utils.VerifyProcessCmdline(pid, ch.chBinaryName(), socketPath(rec.RunDir)) {
		return hypervisor.ErrNotRunning
	}
	return fn(pid)
}

// toVM converts a VMRecord to an external types.VM with runtime info.
func toVM(rec *hypervisor.VMRecord) *types.VM {
	info := rec.VM // value copy — detached from the DB record
	info.SocketPath = socketPath(rec.RunDir)
	info.PID, _ = utils.ReadPIDFile(pidFile(rec.RunDir))
	return &info
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

// runtimeFiles are the per-VM files created at start time
// and removed at stop / cleanup.
var runtimeFiles = []string{"api.sock", "ch.pid", "cmdline", "console.sock"}

func cleanupRuntimeFiles(runDir string) {
	for _, name := range runtimeFiles {
		_ = os.Remove(filepath.Join(runDir, name))
	}
}

func removeVMDirs(runDir, logDir string) error {
	return errors.Join(
		os.RemoveAll(runDir),
		os.RemoveAll(logDir),
	)
}

// rollbackCreate removes a placeholder VM record from the DB.
func (ch *CloudHypervisor) rollbackCreate(ctx context.Context, id, name string) {
	if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		delete(idx.VMs, id)
		delete(idx.Names, name)
		return nil
	}); err != nil {
		log.WithFunc("cloudhypervisor.rollbackCreate").Warnf(ctx, "rollback VM %s (name=%s): %v", id, name, err)
	}
}
