package cloudhypervisor

import (
	"context"
	"net/http"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// Stop shuts down each CH process: UEFI uses ACPI power-button; direct-boot uses vm.shutdown. Both fall back to SIGTERM→SIGKILL.
func (ch *CloudHypervisor) Stop(ctx context.Context, refs []string) ([]string, error) {
	return ch.StopAll(ctx, refs, ch.stopOne)
}

func (ch *CloudHypervisor) stopOne(ctx context.Context, id string) error {
	rec, err := ch.LoadRecord(ctx, id)
	if err != nil {
		return err
	}

	sockPath := hypervisor.SocketPath(rec.RunDir)
	hc := utils.NewSocketHTTPClient(sockPath)
	stopTimeout := time.Duration(ch.conf.StopTimeoutSeconds) * time.Second

	shutdownErr := ch.WithRunningVM(ctx, &rec, func(pid int) error {
		if isDirectBoot(rec.BootConfig) || stopTimeout < 0 /* --force */ {
			return ch.forceTerminate(ctx, hc, id, sockPath, pid)
		}
		return ch.shutdownUEFI(ctx, hc, id, sockPath, pid, stopTimeout)
	})

	return ch.HandleStopResult(ctx, id, rec.RunDir, runtimeFiles, shutdownErr)
}

// shutdownUEFI shuts down a UEFI-boot VM via ACPI power-button with
// poll-and-escalate handled by the shared GracefulStop helper.
func (ch *CloudHypervisor) shutdownUEFI(ctx context.Context, hc *http.Client, vmID, socketPath string, pid int, timeout time.Duration) error {
	return ch.GracefulStop(ctx, vmID, pid, timeout,
		func() error { return powerButton(ctx, hc) },
		func() error { return ch.forceTerminate(ctx, hc, vmID, socketPath, pid) },
	)
}

// forceTerminate flushes disks via REST then SIGTERM→SIGKILL; verifies pid is still cloud-hypervisor to avoid signaling a reused PID.
func (ch *CloudHypervisor) forceTerminate(ctx context.Context, hc *http.Client, vmID, socketPath string, pid int) error {
	if err := shutdownVM(ctx, hc); err != nil {
		log.WithFunc("cloudhypervisor.forceTerminate").Warnf(ctx, "vm.shutdown %s: %v", vmID, err)
	}
	return utils.TerminateProcess(ctx, pid, ch.conf.BinaryName(), socketPath, ch.conf.TerminateGracePeriod())
}

// isDirectBoot returns true when the VM was started with a direct kernel boot
// (OCI images). False means UEFI boot (cloudimg).
func isDirectBoot(boot *types.BootConfig) bool {
	return boot != nil && boot.KernelPath != ""
}
