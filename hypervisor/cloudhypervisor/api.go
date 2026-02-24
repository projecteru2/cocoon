package cloudhypervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
)

type chVMConfig struct {
	Payload *chPayload `json:"payload,omitempty"`
	CPUs    chCPUs     `json:"cpus"`
	Memory  chMemory   `json:"memory"`
	Disks   []chDisk   `json:"disks,omitempty"`
	RNG     *chRNG     `json:"rng,omitempty"`
	Serial  chSerial   `json:"serial"`
	Console chConsole  `json:"console"`
}

type chRNG struct {
	Src string `json:"src"`
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
		RNG:     &chRNG{Src: "/dev/urandom"},
		Serial:  chSerial{Mode: "File", File: serialLogPath},
		Console: chConsole{Mode: "Off"},
	}

	for _, sc := range rec.StorageConfigs {
		cfg.Disks = append(cfg.Disks, storageConfigToDisk(sc))
	}

	if boot := rec.BootConfig; boot != nil {
		switch {
		case boot.KernelPath != "":
			cfg.Payload = &chPayload{
				Kernel:    boot.KernelPath,
				Initramfs: boot.InitrdPath,
				Cmdline:   boot.Cmdline,
			}
		case boot.FirmwarePath != "":
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
		backingFiles = !sc.RO
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
	return hypervisor.DoWithRetry(ctx, func() error {
		return hypervisor.DoPUT(ctx, socketPath, "/api/v1/vm.create", body)
	})
}

func bootVM(ctx context.Context, socketPath string) error {
	return hypervisor.DoWithRetry(ctx, func() error {
		return hypervisor.DoPUT(ctx, socketPath, "/api/v1/vm.boot", nil)
	})
}

func shutdownVM(ctx context.Context, socketPath string) error {
	return hypervisor.DoWithRetry(ctx, func() error {
		return hypervisor.DoPUT(ctx, socketPath, "/api/v1/vm.shutdown", nil)
	})
}

func powerButton(ctx context.Context, socketPath string) error {
	return hypervisor.DoWithRetry(ctx, func() error {
		return hypervisor.DoPUT(ctx, socketPath, "/api/v1/vm.power-button", nil)
	})
}

func isAlreadyCreated(err error) bool {
	var ae *hypervisor.APIError
	return errors.As(err, &ae) &&
		ae.Code == http.StatusInternalServerError &&
		strings.Contains(strings.ToLower(ae.Message), "vm is already created")
}

func isAlreadyBooted(err error) bool {
	var ae *hypervisor.APIError
	return errors.As(err, &ae) &&
		ae.Code == http.StatusInternalServerError &&
		strings.Contains(strings.ToLower(ae.Message), "running to running")
}
