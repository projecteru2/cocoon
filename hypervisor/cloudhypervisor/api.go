package cloudhypervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
)

const httpTimeout = 30 * time.Second

type chVMConfig struct {
	Payload *chPayload `json:"payload,omitempty"`
	CPUs    chCPUs     `json:"cpus"`
	Memory  chMemory   `json:"memory"`
	Disks   []chDisk   `json:"disks,omitempty"`
	Serial  chSerial   `json:"serial"`
	Console chConsole  `json:"console"`
}

type chPayload struct {
	Firmware  string `json:"firmware,omitempty"`
	Kernel    string `json:"kernel,omitempty"`
	Initramfs string `json:"initramfs,omitempty"`
	Cmdline   string `json:"cmdline,omitempty"`
}

type chCPUs struct {
	BootVCPUs int `json:"boot_vcpus"`
	MaxVCPUs  int `json:"max_vcpus"`
}

type chMemory struct {
	Size int64 `json:"size"`
}

type chDisk struct {
	Path         string `json:"path"`
	ReadOnly     bool   `json:"readonly,omitempty"`
	ImageType    string `json:"image_type,omitempty"`    // "Raw", "Qcow2"
	BackingFiles bool   `json:"backing_files,omitempty"` // true for qcow2 COW overlays
	Serial       string `json:"serial,omitempty"`
}

type chSerial struct {
	Mode string `json:"mode"`
	File string `json:"file,omitempty"`
}

type chConsole struct {
	Mode string `json:"mode"`
}

func buildVMConfig(rec *hypervisor.VMRecord, serialLogPath string) *chVMConfig {
	cfg := &chVMConfig{
		CPUs:    chCPUs{BootVCPUs: rec.Config.CPU, MaxVCPUs: rec.Config.CPU},
		Memory:  chMemory{Size: rec.Config.Memory},
		Serial:  chSerial{Mode: "File", File: serialLogPath},
		Console: chConsole{Mode: "Off"},
	}

	for _, sc := range rec.StorageConfigs {
		cfg.Disks = append(cfg.Disks, storageConfigToDisk(sc))
	}

	if boot := rec.BootConfig; boot != nil {
		switch {
		case boot.KernelPath != "":
			// Direct boot (OCI images).
			cfg.Payload = &chPayload{
				Kernel:    boot.KernelPath,
				Initramfs: boot.InitrdPath,
				Cmdline:   boot.Cmdline,
			}
		case boot.FirmwarePath != "":
			// UEFI boot (cloud images).
			cfg.Payload = &chPayload{Firmware: boot.FirmwarePath}
		}
	}

	return cfg
}

func storageConfigToDisk(sc *types.StorageConfig) chDisk {
	imgType := "Raw"
	backingFiles := false
	if filepath.Ext(sc.Path) == ".qcow2" {
		imgType = "Qcow2"
		backingFiles = !sc.RO // writable qcow2 = COW overlay
	}
	return chDisk{
		Path:         sc.Path,
		ReadOnly:     sc.RO,
		ImageType:    imgType,
		BackingFiles: backingFiles,
		Serial:       sc.Serial,
	}
}

func createVM(ctx context.Context, socketPath string, cfg *chVMConfig) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal vm config: %w", err)
	}
	return doPUT(ctx, socketPath, "/api/v1/vm.create", body)
}

func bootVM(ctx context.Context, socketPath string) error {
	return doPUT(ctx, socketPath, "/api/v1/vm.boot", nil)
}

func shutdownVM(ctx context.Context, socketPath string) error {
	return doPUT(ctx, socketPath, "/api/v1/vm.shutdown", nil)
}

func powerButton(ctx context.Context, socketPath string) error {
	return doPUT(ctx, socketPath, "/api/v1/vm.power-button", nil)
}

func doPUT(ctx context.Context, socketPath, path string, body []byte) error {
	hc := newHTTPClient(socketPath)
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://localhost"+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		return &apiError{resp.StatusCode, fmt.Sprintf("PUT %s → %d: %s", path, resp.StatusCode, rb)}
	}
	return nil
}

func newHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: httpTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

func checkSocket(socketPath string) error {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

// apiError carries the HTTP status code for caller classification.
type apiError struct {
	Code    int
	Message string
}

func (e *apiError) Error() string { return e.Message }

// isAlreadyCreated returns true when CH reports the VM is already in CREATED state.
func isAlreadyCreated(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) &&
		ae.Code == http.StatusInternalServerError &&
		strings.Contains(strings.ToLower(ae.Message), "vm is already created")
}

// isAlreadyBooted returns true when CH reports the VM is already running.
func isAlreadyBooted(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) &&
		ae.Code == http.StatusInternalServerError &&
		strings.Contains(strings.ToLower(ae.Message), "running to running")
}
