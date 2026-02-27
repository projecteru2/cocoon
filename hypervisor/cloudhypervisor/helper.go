package cloudhypervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// ReverseLayerSerials extracts read-only layer serial names from StorageConfigs
// and returns them in reverse order (top layer first for overlayfs lowerdir).
func ReverseLayerSerials(storageConfigs []*types.StorageConfig) []string {
	var serials []string
	for _, s := range storageConfigs {
		if s.RO {
			serials = append(serials, s.Serial)
		}
	}
	slices.Reverse(serials)
	return serials
}

// shutdownVM asks Cloud Hypervisor to shut down the guest (flush disk backends).
// Used by the stop flow — the start flow uses CLI args instead of REST API.
func shutdownVM(ctx context.Context, socketPath string) error {
	hc := utils.NewSocketHTTPClient(socketPath)
	return utils.DoWithRetry(ctx, func() error {
		return utils.DoPUT(ctx, hc, "/api/v1/vm.shutdown", nil)
	})
}

// powerButton sends an ACPI power-button event to the guest.
func powerButton(ctx context.Context, socketPath string) error {
	hc := utils.NewSocketHTTPClient(socketPath)
	return utils.DoWithRetry(ctx, func() error {
		return utils.DoPUT(ctx, hc, "/api/v1/vm.power-button", nil)
	})
}

// queryConsolePTY retrieves the virtio-console PTY path from a running CH instance
// via GET /api/v1/vm.info. Returns empty string if the console is not in Pty mode.
func queryConsolePTY(ctx context.Context, apiSocketPath string) (string, error) {
	hc := utils.NewSocketHTTPClient(apiSocketPath)
	body, err := utils.DoGET(ctx, hc, "/api/v1/vm.info")
	if err != nil {
		return "", fmt.Errorf("query vm.info: %w", err)
	}
	var info chVMInfoResponse
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("decode vm.info: %w", err)
	}
	return info.Config.Console.File, nil
}

// blobHexFromPath extracts the digest hex from a blob file path.
// e.g., "/var/lib/cocoon/oci/blobs/abc123.erofs" → "abc123"
func blobHexFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// forEachVM runs fn for each ID, collects successes, and logs failures.
// All IDs are attempted (best-effort); errors are logged and collected.
// The returned succeeded slice is always valid, even when err != nil.
func forEachVM(ctx context.Context, ids []string, op string, fn func(context.Context, string) error) ([]string, error) {
	logger := log.WithFunc("cloudhypervisor." + op)
	var succeeded []string
	var errs []error
	for _, id := range ids {
		if err := fn(ctx, id); err != nil {
			logger.Warnf(ctx, "%s VM %s: %v", op, id, err)
			errs = append(errs, fmt.Errorf("VM %s: %w", id, err))
			continue
		}
		succeeded = append(succeeded, id)
	}
	return succeeded, errors.Join(errs...)
}
