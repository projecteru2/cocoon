package cloudhypervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"
	"github.com/vishvananda/netns"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// Start launches the Cloud Hypervisor process for each VM ref.
// Returns the IDs that were successfully started.
func (ch *CloudHypervisor) Start(ctx context.Context, refs []string) ([]string, error) {
	ids, err := ch.resolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	return forEachVM(ctx, ids, "Start", ch.startOne)
}

func (ch *CloudHypervisor) startOne(ctx context.Context, id string) error {
	rec, err := ch.loadRecord(ctx, id)
	if err != nil {
		return err
	}

	// Idempotent: skip if the VM process is already running regardless of
	// recorded state — prevents double-launch after a state-update failure.
	runErr := ch.withRunningVM(ctx, &rec, func(_ int) error {
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

	// Ensure per-VM runtime and log directories exist (use persisted paths
	// from create time — never overwrite them so cleanup stays consistent).
	if err = utils.EnsureDirs(rec.RunDir, rec.LogDir); err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}

	// Clean up stale runtime files from any previous run.
	cleanupRuntimeFiles(ctx, rec.RunDir)

	socketPath := socketPath(rec.RunDir)
	consoleSock := consoleSockPath(rec.RunDir)

	// Build VM config and convert to CLI args — CH boots immediately on launch.
	vmCfg := buildVMConfig(ctx, &rec, consoleSock)
	args := buildCLIArgs(vmCfg, socketPath)
	ch.saveCmdline(ctx, &rec, args)

	// Launch the CH process with full config.
	withNetwork := len(rec.NetworkConfigs) > 0
	pid, err := ch.launchProcess(ctx, &rec, socketPath, args, withNetwork)
	if err != nil {
		ch.markError(ctx, id)
		return fmt.Errorf("launch VM: %w", err)
	}

	// Persist running state. Console path is resolved lazily by Console() on first access.
	now := time.Now()
	if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r := idx.VMs[id]
		if r == nil {
			return fmt.Errorf("VM %s disappeared from index", id)
		}
		r.State = types.VMStateRunning
		r.StartedAt = &now
		r.UpdatedAt = now
		r.FirstBooted = true
		return nil
	}); err != nil {
		ch.abortLaunch(ctx, pid, socketPath, rec.RunDir)
		return fmt.Errorf("update state: %w", err)
	}
	return nil
}

// launchProcess starts the cloud-hypervisor binary with the given args,
// writes the PID file, waits for the API socket to be ready, then releases
// the process handle so CH lives as an independent OS process past the
// lifetime of this binary.
func (ch *CloudHypervisor) launchProcess(ctx context.Context, rec *hypervisor.VMRecord, socketPath string, args []string, withNetwork bool) (int, error) {
	processLog := filepath.Join(rec.LogDir, "cloud-hypervisor.log")
	logFile, err := os.Create(processLog) //nolint:gosec
	if err != nil {
		log.WithFunc("cloudhypervisor.launchProcess").Warnf(ctx, "create process log: %v", err)
	} else {
		defer func() {
			if closeErr := logFile.Close(); closeErr != nil {
				log.WithFunc("cloudhypervisor.launchProcess").Warnf(ctx, "close log file: %v", closeErr)
			}
		}()
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
		restore, enterErr := enterNetns(rec.NetworkConfigs[0].NetnsPath)
		if enterErr != nil {
			return 0, fmt.Errorf("enter netns: %w", enterErr)
		}
		defer restore()
	}

	if startErr := cmd.Start(); startErr != nil {
		return 0, fmt.Errorf("exec cloud-hypervisor: %w", startErr)
	}
	pid := cmd.Process.Pid

	pidPath := pidFile(rec.RunDir)
	if err := utils.WritePIDFile(pidPath, pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, fmt.Errorf("write PID file: %w", err)
	}

	if err := waitForSocket(ctx, socketPath, pid, ch.conf.SocketWaitTimeout()); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(pidPath)
		return 0, err
	}

	// Reap the child process asynchronously to prevent zombies.
	// In CLI mode init adopts the orphan, but in daemon mode the
	// long-lived parent must wait() or the child becomes a zombie
	// that blocks IsProcessAlive checks during stop/delete.
	go cmd.Wait() //nolint:errcheck
	return pid, nil
}

// waitForSocket polls until socketPath is connectable, the process exits, or
// the timeout/context fires.
func waitForSocket(ctx context.Context, socketPath string, pid int, timeout time.Duration) error {
	return utils.WaitFor(ctx, timeout, 100*time.Millisecond, func() (bool, error) { //nolint:mnd
		if utils.CheckSocket(socketPath) == nil {
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
