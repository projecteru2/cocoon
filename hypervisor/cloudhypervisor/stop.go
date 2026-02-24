package cloudhypervisor

import (
	"context"
	"path/filepath"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

const (
	// acpiPollInterval is how often we check if the guest has powered off
	// after sending an ACPI power-button event.
	acpiPollInterval = 500 * time.Millisecond
	// terminateGracePeriod is the SIGTERM→SIGKILL window.
	terminateGracePeriod = 5 * time.Second
)

// Stop shuts down the Cloud Hypervisor process for each VM ID.
// Two modes are used depending on the VM's boot method:
//   - UEFI boot (cloudimg): ACPI power-button → poll → fallback SIGTERM/SIGKILL
//   - Direct boot (OCI):    vm.shutdown API → SIGTERM → SIGKILL (no ACPI)
//
// Returns the IDs that were successfully stopped.
func (ch *CloudHypervisor) Stop(ctx context.Context, ids []string) ([]string, error) {
	return forEachVM(ctx, ids, "Stop", true, ch.stopOne)
}

func (ch *CloudHypervisor) stopOne(ctx context.Context, id string) error {
	rec, err := ch.loadRecord(ctx, id)
	if err != nil {
		return err
	}
	defer ch.cleanupRuntimeFiles(id)

	pid, _ := utils.ReadPIDFile(ch.conf.CHVMPIDFile(id))
	// Fast path: no running process — just mark stopped.
	if !utils.IsProcessAlive(pid) {
		return ch.updateState(ctx, id, types.VMStateStopped)
	}

	socketPath := ch.conf.CHVMSocketPath(id)
	stopTimeout := time.Duration(ch.conf.StopTimeoutSeconds) * time.Second

	var shutdownErr error
	if isDirectBoot(rec.BootConfig) {
		shutdownErr = ch.forceTerminate(ctx, id, socketPath, pid)
	} else {
		shutdownErr = ch.shutdownUEFI(ctx, id, socketPath, pid, stopTimeout)
	}

	if shutdownErr != nil {
		ch.markError(ctx, id)
		return shutdownErr
	}
	return ch.updateState(ctx, id, types.VMStateStopped)
}

// shutdownUEFI shuts down a UEFI-boot VM:
//  1. Send ACPI power-button — asks the guest OS to shut down cleanly.
//  2. Poll until the process exits or the timeout fires.
//  3. Fallback: forceTerminate (vm.shutdown → SIGTERM → SIGKILL).
func (ch *CloudHypervisor) shutdownUEFI(ctx context.Context, vmID, socketPath string, pid int, timeout time.Duration) error {
	if err := powerButton(ctx, socketPath); err != nil {
		log.WithFunc("cloudhypervisor.shutdownUEFI").Warnf(ctx, "power-button %s: %v — falling back", vmID, err)
		return ch.forceTerminate(ctx, vmID, socketPath, pid)
	}

	// Poll until the process exits or timeout.
	if err := utils.WaitFor(ctx, timeout, acpiPollInterval, func() (bool, error) {
		return !utils.IsProcessAlive(pid), nil
	}); err == nil {
		return nil
	}

	// Guest did not power off in time — escalate.
	log.WithFunc("cloudhypervisor.shutdownUEFI").Warnf(ctx, "VM %s did not respond to power-button within %s — falling back", vmID, timeout)
	return ch.forceTerminate(ctx, vmID, socketPath, pid)
}

// forceTerminate shuts down a VM by flushing disk backends via the REST API,
// then sending SIGTERM → SIGKILL. Verifies the PID still belongs to
// cloud-hypervisor before sending signals to avoid killing a reused PID.
func (ch *CloudHypervisor) forceTerminate(ctx context.Context, vmID, socketPath string, pid int) error {
	if err := shutdownVM(ctx, socketPath); err != nil {
		log.WithFunc("cloudhypervisor.forceTerminate").Warnf(ctx, "vm.shutdown %s: %v", vmID, err)
	}
	if !utils.VerifyProcess(pid, filepath.Base(ch.conf.CHBinary)) {
		return nil
	}
	return utils.TerminateProcess(ctx, pid, terminateGracePeriod)
}

// isDirectBoot returns true when the VM was started with a direct kernel boot
// (OCI images). False means UEFI boot (cloudimg).
func isDirectBoot(boot *types.BootConfig) bool {
	return boot != nil && boot.KernelPath != ""
}
