package cloudhypervisor

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/hypervisor"
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

// Stop shuts down the Cloud Hypervisor process for each VM ref.
// Two modes are used depending on the VM's boot method:
//   - UEFI boot (cloudimg): ACPI power-button → poll → fallback SIGTERM/SIGKILL
//   - Direct boot (OCI):    vm.shutdown API → SIGTERM → SIGKILL (no ACPI)
//
// Returns the IDs that were successfully stopped.
func (ch *CloudHypervisor) Stop(ctx context.Context, refs []string) ([]string, error) {
	ids, err := ch.resolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	return forEachVM(ctx, ids, "Stop", true, ch.stopOne)
}

func (ch *CloudHypervisor) stopOne(ctx context.Context, id string) error {
	rec, err := ch.loadRecord(ctx, id)
	if err != nil {
		return err
	}

	socketPath := ch.conf.CHVMSocketPath(id)
	stopTimeout := time.Duration(ch.conf.StopTimeoutSeconds) * time.Second

	shutdownErr := ch.withRunningVM(id, func(pid int) error {
		if isDirectBoot(rec.BootConfig) {
			return ch.forceTerminate(ctx, id, socketPath, pid)
		}
		return ch.shutdownUEFI(ctx, id, socketPath, pid, stopTimeout)
	})

	switch {
	case errors.Is(shutdownErr, hypervisor.ErrNotRunning):
		// Fast path: no running process — clean up and mark stopped.
		ch.cleanupRuntimeFiles(id)
		return ch.updateState(ctx, id, types.VMStateStopped)
	case shutdownErr != nil:
		// Stop failed — do NOT clean runtime files; the process may still be
		// running and we need socket/PID to control it later.
		ch.markError(ctx, id)
		return shutdownErr
	default:
		ch.cleanupRuntimeFiles(id)
		return ch.updateState(ctx, id, types.VMStateStopped)
	}
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
	return utils.TerminateProcess(ctx, pid, filepath.Base(ch.conf.CHBinary), socketPath, terminateGracePeriod)
}

// isDirectBoot returns true when the VM was started with a direct kernel boot
// (OCI images). False means UEFI boot (cloudimg).
func isDirectBoot(boot *types.BootConfig) bool {
	return boot != nil && boot.KernelPath != ""
}
