package cloudhypervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

const (
	cmdlineFileName = "cmdline"

	// chMemoryRestoreOnDemand uses userfaultfd (UFFD) to lazily page in
	// guest memory from the snapshot file, avoiding a full upfront copy.
	chMemoryRestoreOnDemand chMemoryRestoreMode = "OnDemand"
)

var runtimeFiles = []string{hypervisor.APISocketName, "ch.pid", hypervisor.ConsoleSockName, cmdlineFileName}

// chMemoryRestoreMode controls how CH restores guest memory from a snapshot.
type chMemoryRestoreMode string

type chRestoreConfig struct {
	SourceURL         string              `json:"source_url"`
	MemoryRestoreMode chMemoryRestoreMode `json:"memory_restore_mode,omitempty"`
}

// ReverseLayerSerials extracts layer serials, reversed for overlayfs lowerdir.
func ReverseLayerSerials(storageConfigs []*types.StorageConfig) []string {
	return hypervisor.ReverseLayers(storageConfigs, func(_ int, sc *types.StorageConfig) string { return sc.Serial })
}

// validateSnapshotIntegrity is the CH preflight: common checks, sidecar/
// config.json disk shape match, plus state.json + memory-range-* presence
// so vm.restore won't fail post-kill.
func validateSnapshotIntegrity(srcDir string, sidecar []*types.StorageConfig) error {
	if err := hypervisor.ValidateSnapshotIntegrity(srcDir, sidecar); err != nil {
		return err
	}
	chCfg, _, err := parseCHConfig(filepath.Join(srcDir, "config.json"))
	if err != nil {
		return fmt.Errorf("parse snapshot config: %w", err)
	}
	if len(sidecar) != len(chCfg.Disks) {
		return fmt.Errorf("sidecar/config.json mismatch: %d vs %d disks", len(sidecar), len(chCfg.Disks))
	}
	// writeSnapshotMeta builds the sidecar by walking chCfg.Disks in order, so
	// sidecar[i] and chCfg.Disks[i] must agree on path and readonly. Tampered or
	// imported sidecars whose order drifts would otherwise let patchCHConfig
	// write the wrong path into the wrong disk slot.
	for i, sc := range sidecar {
		if sc.Path != chCfg.Disks[i].Path {
			return fmt.Errorf("sidecar/config.json disk[%d] path mismatch: %q vs %q", i, sc.Path, chCfg.Disks[i].Path)
		}
		if sc.RO != chCfg.Disks[i].ReadOnly {
			return fmt.Errorf("sidecar/config.json disk[%d] readonly mismatch: sidecar=%v config=%v", i, sc.RO, chCfg.Disks[i].ReadOnly)
		}
	}
	if _, statErr := os.Stat(filepath.Join(srcDir, "state.json")); statErr != nil {
		return fmt.Errorf("state.json missing: %w", statErr)
	}
	hasMemory, memErr := hasMemoryRangeFile(srcDir)
	if memErr != nil {
		return fmt.Errorf("read snapshot dir: %w", memErr)
	}
	if !hasMemory {
		return fmt.Errorf("no memory-range-* file in snapshot")
	}
	return nil
}

// hasMemoryRangeFile reports whether srcDir has at least one CH
// memory-range-* file. A missing prefix is enough to fail vm.restore.
func hasMemoryRangeFile(srcDir string) (bool, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "memory-range") {
			return true, nil
		}
	}
	return false, nil
}

func vmAPI(ctx context.Context, hc *http.Client, endpoint string, body []byte, successCodes ...int) error {
	_, err := vmAPICall(ctx, hc, endpoint, body, successCodes...)
	return err
}

// vmAPICall returns the raw response body so callers that need to decode
// PciDeviceInfo (vm.add-fs, vm.add-device, ...) can use the same retry path.
func vmAPICall(ctx context.Context, hc *http.Client, endpoint string, body []byte, successCodes ...int) ([]byte, error) {
	return utils.DoAPIWithRetry(ctx, hc, http.MethodPut, "http://localhost/api/v1/"+endpoint, body, successCodes...)
}

func shutdownVM(ctx context.Context, hc *http.Client) error {
	return vmAPI(ctx, hc, "vm.shutdown", nil)
}

func pauseVM(ctx context.Context, hc *http.Client) error {
	return vmAPI(ctx, hc, "vm.pause", nil)
}

func resumeVM(ctx context.Context, hc *http.Client) error {
	return vmAPI(ctx, hc, "vm.resume", nil)
}

// snapshotVM and restoreVM temporarily extend the client timeout for
// long-running memory transfers, then restore it for subsequent calls.
func snapshotVM(ctx context.Context, hc *http.Client, destDir string) error {
	hc.Timeout = hypervisor.VMMemTransferTimeout
	defer func() { hc.Timeout = utils.HTTPTimeout }()
	body, err := json.Marshal(map[string]string{
		"destination_url": "file://" + destDir,
	})
	if err != nil {
		return fmt.Errorf("marshal snapshot request: %w", err)
	}
	_, err = utils.DoAPI(ctx, hc, http.MethodPut,
		"http://localhost/api/v1/vm.snapshot", body, http.StatusNoContent)
	return err
}

func restoreVM(ctx context.Context, hc *http.Client, sourceDir string, onDemand bool) error {
	hc.Timeout = hypervisor.VMMemTransferTimeout
	defer func() { hc.Timeout = utils.HTTPTimeout }()
	cfg := chRestoreConfig{
		SourceURL: "file://" + sourceDir,
	}
	if onDemand {
		cfg.MemoryRestoreMode = chMemoryRestoreOnDemand
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal restore request: %w", err)
	}
	_, err = utils.DoAPI(ctx, hc, http.MethodPut,
		"http://localhost/api/v1/vm.restore", body, http.StatusNoContent)
	return err
}

func addDiskVM(ctx context.Context, hc *http.Client, disk chDisk) error {
	body, err := json.Marshal(disk)
	if err != nil {
		return fmt.Errorf("marshal add-disk request: %w", err)
	}
	return vmAPI(ctx, hc, "vm.add-disk", body, http.StatusOK, http.StatusNoContent)
}

func removeDeviceVM(ctx context.Context, hc *http.Client, deviceID string) error {
	body, err := json.Marshal(map[string]string{"id": deviceID})
	if err != nil {
		return fmt.Errorf("marshal remove-device request: %w", err)
	}
	return vmAPI(ctx, hc, "vm.remove-device", body)
}

func addNetVM(ctx context.Context, hc *http.Client, net chNet) error {
	body, err := json.Marshal(net)
	if err != nil {
		return fmt.Errorf("marshal add-net request: %w", err)
	}
	return vmAPI(ctx, hc, "vm.add-net", body, http.StatusOK, http.StatusNoContent)
}

// getVMInfo fetches vm.info; cocoon uses it to detect tag/id conflicts
// before hot-add and to surface attached devices through inspect.
func getVMInfo(ctx context.Context, hc *http.Client) (*chVMInfoResponse, error) {
	body, err := utils.DoAPI(ctx, hc, http.MethodGet, "http://localhost/api/v1/vm.info", nil, http.StatusOK)
	if err != nil {
		return nil, fmt.Errorf("query vm.info: %w", err)
	}
	var info chVMInfoResponse
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("decode vm.info: %w", err)
	}
	return &info, nil
}

func decodePciDeviceInfo(resp []byte) (chPciDeviceInfo, error) {
	if len(resp) == 0 {
		return chPciDeviceInfo{}, nil
	}
	var info chPciDeviceInfo
	if err := json.Unmarshal(resp, &info); err != nil {
		return chPciDeviceInfo{}, fmt.Errorf("decode PciDeviceInfo: %w", err)
	}
	return info, nil
}

func powerButton(ctx context.Context, hc *http.Client) error {
	return vmAPI(ctx, hc, "vm.power-button", nil)
}

// queryConsolePTY retrieves the virtio-console PTY path from a running CH
// instance via GET /api/v1/vm.info. Returns empty string if the console is
// not in Pty mode.
func queryConsolePTY(ctx context.Context, apiSocketPath string) (string, error) {
	info, err := getVMInfo(ctx, utils.NewSocketHTTPClient(apiSocketPath))
	if err != nil {
		return "", err
	}
	if info.Config.Console.File == "" {
		return "", fmt.Errorf("console PTY not available (mode=%s)", info.Config.Console.Mode)
	}
	return info.Config.Console.File, nil
}

// resolveConsole determines the console path for a VM after launch.
// Direct-boot (OCI) VMs use a PTY allocated by CH; UEFI VMs use a Unix socket.
func resolveConsole(ctx context.Context, vmID, sockPath, consoleSock string, directBoot bool) string {
	if directBoot {
		consolePath, err := utils.DoWithRetry(ctx, func() (string, error) {
			return queryConsolePTY(ctx, sockPath)
		})
		if err != nil {
			log.WithFunc("cloudhypervisor.resolveConsole").Warnf(ctx, "query console PTY for %s: %v", vmID, err)
		}
		return consolePath
	}
	return consoleSock
}
