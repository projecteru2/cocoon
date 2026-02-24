package cloudhypervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
	"github.com/projecteru2/core/log"
)

const socketWaitTimeout = 5 * time.Second

// Start launches the Cloud Hypervisor process for each VM ref.
// Returns the IDs that were successfully started.
func (ch *CloudHypervisor) Start(ctx context.Context, refs []string) ([]string, error) {
	ids, err := ch.resolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	return forEachVM(ctx, ids, "Start", true, ch.startOne)
}

func (ch *CloudHypervisor) startOne(ctx context.Context, id string) error {
	rec, err := ch.loadRecord(ctx, id)
	if err != nil {
		return err
	}

	// Idempotent: skip if the VM process is already running regardless of
	// recorded state — prevents double-launch after a state-update failure.
	pid, _ := utils.ReadPIDFile(ch.conf.CHVMPIDFile(id))
	if ch.verifyVMProcess(pid, id) {
		if rec.State != types.VMStateRunning {
			_ = ch.updateState(ctx, id, types.VMStateRunning)
		}
		return nil
	}

	// Ensure per-VM runtime and log directories.
	if err := ch.conf.EnsureCHVMDirs(id); err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}

	socketPath := ch.conf.CHVMSocketPath(id)

	// Clean up stale runtime files from any previous run.
	ch.cleanupRuntimeFiles(id)

	// Build VM config and convert to CLI args — CH boots immediately on launch.
	vmCfg := buildVMConfig(&rec, ch.conf.CHVMSerialLog(id))
	args := buildCLIArgs(vmCfg, socketPath)
	ch.saveCmdline(id, args)

	// Launch the CH process with full config.
	pid, err = ch.launchProcess(ctx, id, socketPath, args)
	if err != nil {
		ch.markError(ctx, id)
		return fmt.Errorf("launch VM: %w", err)
	}

	if err := ch.updateState(ctx, id, types.VMStateRunning); err != nil {
		// Kill orphan process — the DB doesn't know the VM is running,
		// so clean up and let the next start retry from a clean slate.
		_ = utils.TerminateProcess(ctx, pid, filepath.Base(ch.conf.CHBinary), socketPath, terminateGracePeriod)
		ch.cleanupRuntimeFiles(id)
		return fmt.Errorf("update state: %w", err)
	}
	return nil
}

// launchProcess starts the cloud-hypervisor binary with the given args,
// writes the PID file, waits for the API socket to be ready, then releases
// the process handle so CH lives as an independent OS process past the
// lifetime of this binary.
func (ch *CloudHypervisor) launchProcess(ctx context.Context, vmID, socketPath string, args []string) (int, error) {
	logFile, err := os.Create(ch.conf.CHVMProcessLog(vmID)) //nolint:gosec
	if err != nil {
		log.WithFunc("cloudhypervisor.launchProcess").Warnf(ctx, "create process log: %v", err)
	} else {
		defer logFile.Close() //nolint:errcheck
	}

	cmd := exec.Command(ch.conf.CHBinary, args...) //nolint:gosec
	// Detach from the parent process group so CH survives if this process exits.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("exec cloud-hypervisor: %w", err)
	}
	pid := cmd.Process.Pid

	if err := utils.WritePIDFile(ch.conf.CHVMPIDFile(vmID), pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, fmt.Errorf("write PID file: %w", err)
	}

	if err := waitForSocket(ctx, socketPath, pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(ch.conf.CHVMPIDFile(vmID))
		return 0, err
	}

	// Release the process handle: CH is fully detached from Go runtime.
	_ = cmd.Process.Release()
	return pid, nil
}

// waitForSocket polls until socketPath is connectable, the process exits, or
// the timeout/context fires.
func waitForSocket(ctx context.Context, socketPath string, pid int) error {
	return utils.WaitFor(ctx, socketWaitTimeout, 100*time.Millisecond, func() (bool, error) { //nolint:mnd
		if hypervisor.CheckSocket(socketPath) == nil {
			return true, nil
		}
		if !utils.IsProcessAlive(pid) {
			return false, fmt.Errorf("cloud-hypervisor exited before socket was ready")
		}
		return false, nil
	})
}
