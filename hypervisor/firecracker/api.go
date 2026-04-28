package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/utils"
)

// FC REST API constants.
const (
	actionInstanceStart  = "InstanceStart"
	actionSendCtrlAltDel = "SendCtrlAltDel"
	vmStatePaused        = "Paused"
	vmStateResumed       = "Resumed"
	memBackendTypeFile   = "File"

	driveIDFmt = "drive_%d"
	ifaceIDFmt = "eth%d"

	cowFileName = "cow.raw"

	hugePagesNone = "None"
	hugePages2M   = "2M"
	ioEngineAsync = "Async" // io_uring
)

// Firecracker REST API request types.
// FC uses a pre-boot configuration model: start an empty process,
// configure via HTTP PUT/PATCH, then issue InstanceStart.

type fcMachineConfig struct {
	VCPUCount  int    `json:"vcpu_count"`
	MemSizeMiB int    `json:"mem_size_mib"`
	HugePages  string `json:"huge_pages,omitempty"`
}

type fcBootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	InitrdPath      string `json:"initrd_path,omitempty"`
	BootArgs        string `json:"boot_args,omitempty"`
}

type fcDrive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
	IoEngine     string `json:"io_engine,omitempty"`
}

type fcNetworkInterface struct {
	IfaceID     string `json:"iface_id"`
	HostDevName string `json:"host_dev_name"`
	GuestMAC    string `json:"guest_mac,omitempty"`
}

type fcAction struct {
	ActionType string `json:"action_type"`
}

type fcBalloon struct {
	AmountMiB         int  `json:"amount_mib"`
	DeflateOnOOM      bool `json:"deflate_on_oom,omitempty"`
	FreePageReporting bool `json:"free_page_reporting,omitempty"`
}

// FC snapshot/load request types.
type fcSnapshotCreate struct {
	SnapshotPath string `json:"snapshot_path"`
	MemFilePath  string `json:"mem_file_path"`
}

type fcSnapshotLoad struct {
	SnapshotPath        string              `json:"snapshot_path"`
	MemBackend          fcSnapshotMemBE     `json:"mem_backend"`
	EnableDiffSnapshots bool                `json:"enable_diff_snapshots,omitempty"`
	ResumeVM            bool                `json:"resume_vm,omitempty"`
	NetworkOverrides    []fcNetworkOverride `json:"network_overrides,omitempty"`
}

// fcNetworkOverride overrides a network interface from the snapshot
// with a new TAP device (FC v1.14+, PR #4731).
type fcNetworkOverride struct {
	IfaceID     string `json:"iface_id"`
	HostDevName string `json:"host_dev_name"`
}

type fcSnapshotMemBE struct {
	BackendPath string `json:"backend_path"`
	BackendType string `json:"backend_type"`
}

// fcAPI PUTs body to a Firecracker REST endpoint with retry. Use only for
// idempotent endpoints (pre-boot config — putMachineConfig, putBootSource,
// putDrive, putNetworkInterface, putBalloon, putEntropy — where a retry
// PUT just overwrites the same config). All current callers expect 204; if
// a future endpoint needs alt success codes, switch to DoAPIWithRetry
// directly instead of widening this wrapper.
func fcAPI(ctx context.Context, hc *http.Client, endpoint string, body []byte) error {
	_, err := utils.DoAPIWithRetry(ctx, hc, http.MethodPut, "http://localhost"+endpoint, body)
	return err
}

// fcAPIOnce is the no-retry variant for non-idempotent state transitions
// (instance-start, pause/resume) where a retry after a lost response would
// hit a wrong-state error and mask the original success.
func fcAPIOnce(ctx context.Context, hc *http.Client, method, endpoint string, body []byte, successCodes ...int) error {
	_, err := utils.DoAPIOnce(ctx, hc, method, "http://localhost"+endpoint, body, successCodes...)
	return err
}

// putJSON marshals payload and PUTs it to endpoint via fcAPI.
func putJSON[T any](ctx context.Context, hc *http.Client, endpoint string, payload T, kind string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", kind, err)
	}
	return fcAPI(ctx, hc, endpoint, body)
}

func putMachineConfig(ctx context.Context, hc *http.Client, cfg fcMachineConfig) error {
	return putJSON(ctx, hc, "/machine-config", cfg, "machine-config")
}

func putBootSource(ctx context.Context, hc *http.Client, boot fcBootSource) error {
	return putJSON(ctx, hc, "/boot-source", boot, "boot-source")
}

func putDrive(ctx context.Context, hc *http.Client, drive fcDrive) error {
	return putJSON(ctx, hc, "/drives/"+drive.DriveID, drive, "drive")
}

func putBalloon(ctx context.Context, hc *http.Client, balloon fcBalloon) error {
	return putJSON(ctx, hc, "/balloon", balloon, "balloon")
}

func putNetworkInterface(ctx context.Context, hc *http.Client, iface fcNetworkInterface) error {
	return putJSON(ctx, hc, "/network-interfaces/"+iface.IfaceID, iface, "network-interface")
}

func putEntropy(ctx context.Context, hc *http.Client) error {
	return fcAPI(ctx, hc, "/entropy", []byte("{}"))
}

func instanceStart(ctx context.Context, hc *http.Client) error {
	body, err := json.Marshal(fcAction{ActionType: actionInstanceStart})
	if err != nil {
		return fmt.Errorf("marshal action: %w", err)
	}
	return fcAPIOnce(ctx, hc, http.MethodPut, "/actions", body, http.StatusNoContent, http.StatusOK)
}

func sendCtrlAltDel(ctx context.Context, hc *http.Client) error {
	body, err := json.Marshal(fcAction{ActionType: actionSendCtrlAltDel})
	if err != nil {
		return fmt.Errorf("marshal action: %w", err)
	}
	return fcAPIOnce(ctx, hc, http.MethodPut, "/actions", body)
}

// pauseVM pauses a running FC instance via PATCH /vm. Non-idempotent: a
// retry after a lost response would hit "already paused" and mask success.
func pauseVM(ctx context.Context, hc *http.Client) error {
	body, err := json.Marshal(map[string]string{"state": vmStatePaused})
	if err != nil {
		return fmt.Errorf("marshal pause request: %w", err)
	}
	return fcAPIOnce(ctx, hc, http.MethodPatch, "/vm", body)
}

// resumeVM resumes a paused FC instance via PATCH /vm. Same non-idempotent
// shape as pauseVM.
func resumeVM(ctx context.Context, hc *http.Client) error {
	body, err := json.Marshal(map[string]string{"state": vmStateResumed})
	if err != nil {
		return fmt.Errorf("marshal resume request: %w", err)
	}
	return fcAPIOnce(ctx, hc, http.MethodPatch, "/vm", body)
}

// createSnapshotFC creates a full VM snapshot (vmstate + memory file) in destDir.
// Bypasses retry — memory transfer takes minutes; resending after a transient
// error would re-transfer multi-GiB and overwrite a partial state.json.
func createSnapshotFC(ctx context.Context, sockPath, destDir string) error {
	body, err := json.Marshal(fcSnapshotCreate{
		SnapshotPath: filepath.Join(destDir, snapshotVMStateFile),
		MemFilePath:  filepath.Join(destDir, snapshotMemFile),
	})
	if err != nil {
		return fmt.Errorf("marshal snapshot/create request: %w", err)
	}
	hc := utils.NewSocketHTTPClientWithTimeout(sockPath, hypervisor.VMMemTransferTimeout)
	_, err = utils.DoAPIOnce(ctx, hc, http.MethodPut,
		"http://localhost/snapshot/create", body, http.StatusNoContent)
	return err
}

// loadSnapshotFC loads a VM snapshot from sourceDir into a freshly started FC process.
// networkOverrides replaces TAP devices from the snapshot with new ones.
// Bypasses retry — same memory-transfer reasoning as createSnapshotFC.
func loadSnapshotFC(ctx context.Context, sockPath, sourceDir string, networkOverrides []fcNetworkOverride) error {
	body, err := json.Marshal(fcSnapshotLoad{
		SnapshotPath: filepath.Join(sourceDir, snapshotVMStateFile),
		MemBackend: fcSnapshotMemBE{
			BackendPath: filepath.Join(sourceDir, snapshotMemFile),
			BackendType: memBackendTypeFile,
		},
		NetworkOverrides: networkOverrides,
	})
	if err != nil {
		return fmt.Errorf("marshal snapshot/load request: %w", err)
	}
	hc := utils.NewSocketHTTPClientWithTimeout(sockPath, hypervisor.VMMemTransferTimeout)
	_, err = utils.DoAPIOnce(ctx, hc, http.MethodPut,
		"http://localhost/snapshot/load", body, http.StatusNoContent)
	return err
}
