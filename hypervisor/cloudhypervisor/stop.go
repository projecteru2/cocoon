package cloudhypervisor

import (
	"context"
	"fmt"
	"os"
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
	logger := log.WithFunc("cloudhypervisor.Stop")
	var stopped []string
	for _, id := range ids {
		if err := ch.stopOne(ctx, id); err != nil {
			logger.Warnf(ctx, "stop VM %s: %v", id, err)
			continue
		}
		stopped = append(stopped, id)
	}
	return stopped, nil
}

func (ch *CloudHypervisor) stopOne(ctx context.Context, id string) error {
	rec, err := ch.loadRecord(ctx, id)
	if err != nil {
		return err
	}

	socketPath := ch.conf.CHVMSocketPath(id)
	pid, _ := utils.ReadPIDFile(ch.conf.CHVMPIDFile(id))

	// Fast path: no running process — just clean up and mark stopped.
	if !utils.IsProcessAlive(pid) {
		_ = os.Remove(socketPath)
		_ = os.Remove(ch.conf.CHVMPIDFile(id))
		return ch.updateState(ctx, id, types.VMStateStopped)
	}

	stopTimeout := time.Duration(ch.conf.StopTimeoutSeconds) * time.Second

	var shutdownErr error
	if isDirectBoot(rec.BootConfig) {
		shutdownErr = ch.shutdownDirect(ctx, id, socketPath, pid)
	} else {
		shutdownErr = ch.shutdownUEFI(ctx, id, socketPath, pid, stopTimeout)
	}

	// Clean up runtime files regardless of how shutdown went.
	_ = os.Remove(socketPath)
	_ = os.Remove(ch.conf.CHVMPIDFile(id))

	if shutdownErr != nil {
		ch.markError(ctx, id)
		return shutdownErr
	}
	return ch.updateState(ctx, id, types.VMStateStopped)
}

// shutdownUEFI shuts down a UEFI-boot VM:
//  1. Send ACPI power-button — asks the guest OS to shut down cleanly.
//  2. Poll until the process exits or the timeout fires.
//  3. Fallback: vm.shutdown API → SIGTERM → SIGKILL.
func (ch *CloudHypervisor) shutdownUEFI(ctx context.Context, vmID, socketPath string, pid int, timeout time.Duration) error {
	if err := powerButton(ctx, socketPath); err != nil {
		log.WithFunc("cloudhypervisor.shutdownUEFI").Warnf(ctx, "power-button %s: %v — falling back", vmID, err)
		return ch.terminateWithFallback(ctx, vmID, socketPath, pid)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !utils.IsProcessAlive(pid) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled waiting for ACPI shutdown: %w", ctx.Err())
		case <-time.After(acpiPollInterval):
		}
	}

	// Guest did not power off in time — escalate.
	log.WithFunc("cloudhypervisor.shutdownUEFI").Warnf(ctx, "VM %s did not respond to power-button within %s — falling back", vmID, timeout)
	return ch.terminateWithFallback(ctx, vmID, socketPath, pid)
}

// shutdownDirect shuts down a direct-boot VM without sending ACPI events:
//
//	vm.shutdown API (flush disk backends) → SIGTERM → SIGKILL.
func (ch *CloudHypervisor) shutdownDirect(ctx context.Context, vmID, socketPath string, pid int) error {
	if err := shutdownVM(ctx, socketPath); err != nil {
		log.WithFunc("cloudhypervisor.shutdownDirect").Warnf(ctx, "vm.shutdown %s: %v — proceeding to SIGTERM", vmID, err)
	}
	return utils.TerminateProcess(pid, terminateGracePeriod)
}

// terminateWithFallback is used when graceful shutdown failed:
// vm.shutdown (flush backends) → SIGTERM → SIGKILL.
func (ch *CloudHypervisor) terminateWithFallback(ctx context.Context, vmID, socketPath string, pid int) error {
	if err := shutdownVM(ctx, socketPath); err != nil {
		log.WithFunc("cloudhypervisor.terminateWithFallback").Warnf(ctx, "vm.shutdown %s: %v", vmID, err)
	}
	return utils.TerminateProcess(pid, terminateGracePeriod)
}

// isDirectBoot returns true when the VM was started with a direct kernel boot
// (OCI images). False means UEFI boot (cloudimg).
func isDirectBoot(boot *types.BootConfig) bool {
	return boot != nil && boot.KernelPath != ""
}
