package cloudhypervisor

import (
	"context"

	"github.com/projecteru2/cocoon/hypervisor"
)

// shutdownVM asks Cloud Hypervisor to shut down the guest (flush disk backends).
// Used by the stop flow — the start flow uses CLI args instead of REST API.
func shutdownVM(ctx context.Context, socketPath string) error {
	return hypervisor.DoWithRetry(ctx, func() error {
		return hypervisor.DoPUT(ctx, socketPath, "/api/v1/vm.shutdown", nil)
	})
}

// powerButton sends an ACPI power-button event to the guest.
func powerButton(ctx context.Context, socketPath string) error {
	return hypervisor.DoWithRetry(ctx, func() error {
		return hypervisor.DoPUT(ctx, socketPath, "/api/v1/vm.power-button", nil)
	})
}
