package cloudhypervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"
	"github.com/vishvananda/netns"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
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
	runErr := ch.withRunningVM(id, func(_ int) error {
		if rec.State != types.VMStateRunning {
			return ch.updateState(ctx, id, types.VMStateRunning)
		}
		return nil
	})
	switch {
	case runErr == nil:
		return nil // already running
	case errors.Is(runErr, hypervisor.ErrNotRunning):
		// expected — proceed to launch
	default:
		// VM is running but state update failed — do not re-launch.
		return fmt.Errorf("reconcile running VM %s: %w", id, runErr)
	}

	// Ensure per-VM runtime and log directories.
	if err = ch.conf.EnsureCHVMDirs(id); err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}

	socketPath := ch.conf.CHVMSocketPath(id)

	// Clean up stale runtime files from any previous run.
	ch.cleanupRuntimeFiles(id)

	// Build VM config and convert to CLI args — CH boots immediately on launch.
	vmCfg := buildVMConfig(&rec, ch.conf.CHVMConsoleSock(id))
	args := buildCLIArgs(vmCfg, socketPath)
	ch.saveCmdline(id, args)

	// Launch the CH process with full config.
	withNetwork := len(rec.NetworkConfigs) > 0
	pid, err := ch.launchProcess(ctx, id, socketPath, args, withNetwork)
	if err != nil {
		ch.markError(ctx, id)
		return fmt.Errorf("launch VM: %w", err)
	}

	var consolePath string
	if isDirectBoot(rec.BootConfig) {
		var ptyErr error
		consolePath, ptyErr = queryConsolePTY(ctx, socketPath)
		if ptyErr != nil {
			log.WithFunc("cloudhypervisor.startOne").Warnf(ctx, "query console PTY for %s: %v", id, ptyErr)
		}
	} else {
		consolePath = ch.conf.CHVMConsoleSock(id)
	}

	// Persist running state + console path.
	now := time.Now()
	if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r := idx.VMs[id]
		if r == nil {
			return fmt.Errorf("VM %s disappeared from index", id)
		}
		r.State = types.VMStateRunning
		r.StartedAt = &now
		r.UpdatedAt = now
		r.ConsolePath = consolePath
		return nil
	}); err != nil {
		_ = utils.TerminateProcess(ctx, pid, ch.chBinaryName(), socketPath, terminateGracePeriod)
		ch.cleanupRuntimeFiles(id)
		return fmt.Errorf("update state: %w", err)
	}
	return nil
}

// launchProcess starts the cloud-hypervisor binary with the given args,
// writes the PID file, waits for the API socket to be ready, then releases
// the process handle so CH lives as an independent OS process past the
// lifetime of this binary.
func (ch *CloudHypervisor) launchProcess(ctx context.Context, vmID, socketPath string, args []string, withNetwork bool) (int, error) {
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

	// If the VM has network, CH must be launched inside the VM's netns
	// so it can access the tap device. We setns before fork and restore after.
	if withNetwork {
		restore, enterErr := enterNetns(ch.conf.CNINetnsPath(vmID))
		if enterErr != nil {
			return 0, fmt.Errorf("enter netns: %w", enterErr)
		}
		defer restore()
	}

	if startErr := cmd.Start(); startErr != nil {
		return 0, fmt.Errorf("exec cloud-hypervisor: %w", startErr)
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

// enterNetns locks the OS thread, saves the current netns, and switches
// to the target netns. The forked child process inherits the new netns.
// Returns a restore function that must be deferred by the caller.
// No global state — safe for concurrent use.
func enterNetns(nsPath string) (restore func(), err error) {
	runtime.LockOSThread()

	orig, err := netns.Get()
	if err != nil {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("get current netns: %w", err)
	}

	target, err := netns.GetFromPath(nsPath)
	if err != nil {
		_ = orig.Close()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("open netns %s: %w", nsPath, err)
	}
	defer target.Close() //nolint:errcheck

	if err := netns.Set(target); err != nil {
		_ = orig.Close()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("setns %s: %w", nsPath, err)
	}

	return func() {
		_ = netns.Set(orig)
		_ = orig.Close()
		runtime.UnlockOSThread()
	}, nil
}
