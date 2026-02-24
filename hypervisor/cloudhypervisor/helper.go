package cloudhypervisor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/core/log"
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

// blobHexFromPath extracts the digest hex from a blob file path.
// e.g., "/var/lib/cocoon/oci/blobs/abc123.erofs" → "abc123"
func blobHexFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// forEachVM runs fn for each ID, collects successes, and logs failures.
// In bestEffort mode all IDs are attempted; errors are logged and collected.
// Otherwise the first error stops processing.
// The returned succeeded slice is always valid, even when err != nil.
func forEachVM(ctx context.Context, ids []string, op string, bestEffort bool, fn func(context.Context, string) error) ([]string, error) {
	logger := log.WithFunc("cloudhypervisor." + op)
	var succeeded []string
	var errs []error
	for _, id := range ids {
		if err := fn(ctx, id); err != nil {
			if !bestEffort {
				return succeeded, fmt.Errorf("%s VM %s: %w", op, id, err)
			}
			logger.Warnf(ctx, "%s VM %s: %v", op, id, err)
			errs = append(errs, fmt.Errorf("VM %s: %w", id, err))
			continue
		}
		succeeded = append(succeeded, id)
	}
	return succeeded, errors.Join(errs...)
}
