package cloudhypervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

const socketWaitTimeout = 5 * time.Second

// Start launches the Cloud Hypervisor process for each VM ID.
// Returns the IDs that were successfully started.
func (ch *CloudHypervisor) Start(ctx context.Context, ids []string) ([]string, error) {
	logger := log.WithFunc("cloudhypervisor.Start")
	var started []string
	for _, id := range ids {
		if err := ch.startOne(ctx, id); err != nil {
			logger.Warnf(ctx, "start VM %s: %v", id, err)
			continue
		}
		started = append(started, id)
	}
	return started, nil
}

func (ch *CloudHypervisor) startOne(ctx context.Context, id string) error {
	rec, err := ch.loadRecord(ctx, id)
	if err != nil {
		return err
	}

	// Idempotent: skip if already running.
	if rec.State == types.VMStateRunning {
		pid, _ := utils.ReadPIDFile(ch.conf.CHVMPIDFile(id))
		if utils.IsProcessAlive(pid) {
			return nil
		}
	}

	// Ensure per-VM runtime and log directories.
	if err := ch.conf.EnsureCHVMDirs(id); err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}

	socketPath := ch.conf.CHVMSocketPath(id)

	// Clean up stale socket and PID file from any previous run.
	_ = os.Remove(socketPath)
	_ = os.Remove(ch.conf.CHVMPIDFile(id))

	// Launch the CH process.
	pid, err := ch.launchProcess(ctx, id, socketPath)
	if err != nil {
		return fmt.Errorf("launch CH process: %w", err)
	}

	// Configure VM via REST API.
	vmCfg := buildVMConfig(&rec, ch.conf.CHVMSerialLog(id))
	if err := ch.callCreateVM(ctx, socketPath, vmCfg); err != nil {
		_ = ch.killProcess(id, pid)
		return fmt.Errorf("vm.create: %w", err)
	}
	if err := ch.callBootVM(ctx, socketPath); err != nil {
		_ = ch.killProcess(id, pid)
		return fmt.Errorf("vm.boot: %w", err)
	}

	// Persist running state.
	now := time.Now()
	return ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r := idx.VMs[id]
		if r == nil {
			return fmt.Errorf("VM %s disappeared from index", id)
		}
		r.State = types.VMStateRunning
		r.StartedAt = &now
		r.UpdatedAt = now
		return nil
	})
}

// launchProcess starts the cloud-hypervisor binary, writes the PID file,
// waits for the API socket to be ready, then releases the process handle
// so CH lives as an independent OS process past the lifetime of this binary.
func (ch *CloudHypervisor) launchProcess(ctx context.Context, vmID, socketPath string) (int, error) {
	logFile, _ := os.Create(ch.conf.CHVMProcessLog(vmID)) //nolint:gosec

	cmd := exec.Command(ch.conf.CHBinary, "--api-socket", socketPath) //nolint:gosec
	// Detach from the parent process group so CH survives if this process exits.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return 0, fmt.Errorf("exec cloud-hypervisor: %w", err)
	}
	pid := cmd.Process.Pid

	if err := utils.WritePIDFile(ch.conf.CHVMPIDFile(vmID), pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if logFile != nil {
			_ = logFile.Close()
		}
		return 0, fmt.Errorf("write PID file: %w", err)
	}

	if err := waitForSocket(ctx, socketPath, socketWaitTimeout, pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if logFile != nil {
			_ = logFile.Close()
		}
		_ = os.Remove(ch.conf.CHVMPIDFile(vmID))
		return 0, err
	}

	// Release the process handle: CH is fully detached from Go runtime.
	_ = cmd.Process.Release()
	if logFile != nil {
		_ = logFile.Close()
	}
	return pid, nil
}

// waitForSocket polls until socketPath is connectable, the process exits, or
// the timeout/context fires.
func waitForSocket(ctx context.Context, socketPath string, timeout time.Duration, pid int) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for socket %s", socketPath)
		}
		if checkSocket(socketPath) == nil {
			return nil
		}
		if !utils.IsProcessAlive(pid) {
			return fmt.Errorf("cloud-hypervisor exited before socket was ready")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled waiting for socket: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// callCreateVM wraps createVM with idempotency: treats "already created" as success.
func (ch *CloudHypervisor) callCreateVM(ctx context.Context, socketPath string, cfg *chVMConfig) error {
	err := createVM(ctx, socketPath, cfg)
	if isAlreadyCreated(err) {
		return nil
	}
	return err
}

// callBootVM wraps bootVM with idempotency: treats "already running" as success.
func (ch *CloudHypervisor) callBootVM(ctx context.Context, socketPath string) error {
	err := bootVM(ctx, socketPath)
	if isAlreadyBooted(err) {
		return nil
	}
	return err
}

// killProcess terminates the CH process for vmID as a cleanup measure after
// a failed start sequence.
func (ch *CloudHypervisor) killProcess(vmID string, pid int) error {
	_ = os.Remove(ch.conf.CHVMSocketPath(vmID))
	_ = os.Remove(ch.conf.CHVMPIDFile(vmID))
	if pid > 0 && utils.IsProcessAlive(pid) {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return nil
		}
		return proc.Kill()
	}
	return nil
}
