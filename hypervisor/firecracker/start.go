package firecracker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// Start launches the Firecracker process for each VM ref, configures it
// via the REST API, and issues InstanceStart.
func (fc *Firecracker) Start(ctx context.Context, refs []string) ([]string, error) {
	ids, err := fc.resolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	succeeded, forEachErr := fc.forEachVM(ctx, ids, "Start", fc.startOne)
	if batchErr := fc.batchMarkStarted(ctx, succeeded); batchErr != nil {
		log.WithFunc("firecracker.Start").Warnf(ctx, "batch state update: %v", batchErr)
	}
	return succeeded, forEachErr
}

func (fc *Firecracker) startOne(ctx context.Context, id string) error {
	rec, err := fc.loadRecord(ctx, id)
	if err != nil {
		return err
	}

	runErr := fc.withRunningVM(ctx, &rec, func(_ int) error { return nil })
	switch {
	case runErr == nil:
		return nil
	case errors.Is(runErr, hypervisor.ErrNotRunning):
	default:
		return fmt.Errorf("reconcile running VM %s: %w", id, runErr)
	}

	if err = utils.EnsureDirs(rec.RunDir, rec.LogDir); err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}

	cleanupRuntimeFiles(ctx, rec.RunDir)

	sockPath := socketPath(rec.RunDir)

	withNetwork := len(rec.NetworkConfigs) > 0
	pid, err := fc.launchProcess(ctx, &rec, sockPath, withNetwork)
	if err != nil {
		fc.markError(ctx, id)
		return fmt.Errorf("launch VM: %w", err)
	}

	// Configure VM via REST API and start the instance.
	if err := fc.configureVM(ctx, utils.NewSocketHTTPClient(sockPath), &rec); err != nil {
		fc.abortLaunch(ctx, pid, sockPath, rec.RunDir)
		fc.markError(ctx, id)
		return fmt.Errorf("configure VM: %w", err)
	}
	return nil
}

// configureVM sends the pre-boot configuration to FC via REST API,
// then issues InstanceStart to boot the guest.
func (fc *Firecracker) configureVM(ctx context.Context, hc *http.Client, rec *hypervisor.VMRecord) error {
	memMiB := int(rec.Config.Memory >> 20) //nolint:mnd
	if err := putMachineConfig(ctx, hc, fcMachineConfig{
		VCPUCount:  rec.Config.CPU,
		MemSizeMiB: memMiB,
		HugePages:  utils.DetectHugePages(),
	}); err != nil {
		return fmt.Errorf("machine-config: %w", err)
	}

	if boot := rec.BootConfig; boot != nil {
		if err := putBootSource(ctx, hc, fcBootSource{
			KernelImagePath: boot.KernelPath,
			InitrdPath:      boot.InitrdPath,
			BootArgs:        boot.Cmdline,
		}); err != nil {
			return fmt.Errorf("boot-source: %w", err)
		}
	}

	for i, sc := range rec.StorageConfigs {
		driveID := fmt.Sprintf("drive-%d", i)
		if err := putDrive(ctx, hc, fcDrive{
			DriveID:      driveID,
			PathOnHost:   sc.Path,
			IsRootDevice: false,
			IsReadOnly:   sc.RO,
		}); err != nil {
			return fmt.Errorf("drive %s: %w", driveID, err)
		}
	}

	for i, nc := range rec.NetworkConfigs {
		ifaceID := fmt.Sprintf("eth%d", i)
		if err := putNetworkInterface(ctx, hc, fcNetworkInterface{
			IfaceID:     ifaceID,
			HostDevName: nc.Tap,
			GuestMAC:    nc.Mac,
		}); err != nil {
			return fmt.Errorf("network-interface %s: %w", ifaceID, err)
		}
	}

	if err := instanceStart(ctx, hc); err != nil {
		return fmt.Errorf("instance-start: %w", err)
	}
	return nil
}

func (fc *Firecracker) batchMarkStarted(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	return fc.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		for _, id := range ids {
			r := idx.VMs[id]
			if r == nil {
				continue
			}
			r.State = types.VMStateRunning
			r.StartedAt = &now
			r.UpdatedAt = now
			r.FirstBooted = true
		}
		return nil
	})
}

// launchProcess starts the firecracker binary with --api-sock,
// writes the PID file, waits for the API socket to be ready, then
// releases the process handle.
func (fc *Firecracker) launchProcess(ctx context.Context, rec *hypervisor.VMRecord, sockPath string, withNetwork bool) (int, error) {
	fcLog := filepath.Join(rec.LogDir, "firecracker.log")

	cmd := exec.Command(fc.conf.FCBinary, //nolint:gosec
		"--api-sock", sockPath,
		"--log-path", fcLog,
		"--level", "Warning",
		"--id", rec.ID,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Serial console (stdin/stdout) is not connected until Console adds PTY.

	if withNetwork {
		restore, enterErr := enterNetns(rec.NetworkConfigs[0].NetnsPath)
		if enterErr != nil {
			return 0, fmt.Errorf("enter netns: %w", enterErr)
		}
		defer restore()
	}

	if startErr := cmd.Start(); startErr != nil {
		return 0, fmt.Errorf("exec firecracker: %w", startErr)
	}
	pid := cmd.Process.Pid

	pidPath := pidFile(rec.RunDir)
	if err := utils.WritePIDFile(pidPath, pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, fmt.Errorf("write PID file: %w", err)
	}

	if err := waitForSocket(ctx, sockPath, pid, fc.conf.SocketWaitTimeout()); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(pidPath)
		return 0, err
	}

	go cmd.Wait() //nolint:errcheck
	return pid, nil
}

// waitForSocket polls until socketPath is connectable, the process exits, or
// the timeout/context fires.
func waitForSocket(ctx context.Context, sockPath string, pid int, timeout time.Duration) error {
	return utils.WaitFor(ctx, timeout, 100*time.Millisecond, func() (bool, error) { //nolint:mnd
		if utils.CheckSocket(sockPath) == nil {
			return true, nil
		}
		if !utils.IsProcessAlive(pid) {
			return false, fmt.Errorf("firecracker exited before socket was ready")
		}
		return false, nil
	})
}

// enterNetns locks the OS thread, saves the current netns, and switches
// to the target netns. The forked child process inherits the new netns.
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
