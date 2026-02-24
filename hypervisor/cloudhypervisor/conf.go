package cloudhypervisor

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

func buildVMConfig(rec *hypervisor.VMRecord, serialLogPath string) *chVMConfig {
	cpu := rec.Config.CPU
	mem := rec.Config.Memory

	maxVCPUs := runtime.NumCPU()
	if cpu > maxVCPUs {
		maxVCPUs = cpu
	}

	cfg := &chVMConfig{
		CPUs:     chCPUs{BootVCPUs: cpu, MaxVCPUs: maxVCPUs},
		Memory:   chMemory{Size: mem, HugePages: utils.DetectHugePages()},
		RNG:      chRNG{Src: "/dev/urandom"},
		Watchdog: true,
		Serial:   chSerial{Mode: "File", File: serialLogPath},
		Console:  chConsole{Mode: "Off"},
	}

	// Balloon: 25% of memory, only when memory >= 256 MiB.
	if mem >= minBalloonMemory {
		cfg.Balloon = &chBalloon{
			Size:              mem / 4, //nolint:mnd
			DeflateOnOOM:      true,
			FreePageReporting: true,
		}
	}

	for _, sc := range rec.StorageConfigs {
		cfg.Disks = append(cfg.Disks, storageConfigToDisk(sc, cpu))
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

func storageConfigToDisk(sc *types.StorageConfig, cpuCount int) chDisk {
	d := chDisk{
		Path:      sc.Path,
		ReadOnly:  sc.RO,
		Serial:    sc.Serial,
		NumQueues: cpuCount,
		QueueSize: 256, //nolint:mnd
	}

	switch {
	case filepath.Ext(sc.Path) == ".qcow2":
		// cloudimg qcow2 overlay
		d.ImageType = "Qcow2"
		d.BackingFiles = !sc.RO
	case sc.RO:
		// OCI EROFS layer: readonly, direct I/O
		d.ImageType = "Raw"
		d.Direct = true
	default:
		// OCI COW raw: writable, direct I/O, sparse
		d.ImageType = "Raw"
		d.Direct = true
		d.Sparse = true
	}
	return d
}

// buildCLIArgs converts a chVMConfig into cloud-hypervisor CLI arguments.
// The resulting args include --api-socket so the socket remains available
// for later control operations (stop, shutdown, power-button).
func buildCLIArgs(cfg *chVMConfig, socketPath string) []string {
	args := []string{"--api-socket", socketPath}

	// --cpus
	args = append(args, "--cpus", fmt.Sprintf("boot=%d,max=%d", cfg.CPUs.BootVCPUs, cfg.CPUs.MaxVCPUs))

	// --memory
	mem := fmt.Sprintf("size=%d", cfg.Memory.Size)
	if cfg.Memory.HugePages {
		mem += ",hugepages=on"
	}
	args = append(args, "--memory", mem)

	// --disk (one per disk)
	for _, d := range cfg.Disks {
		args = append(args, "--disk", diskToCLIArg(d))
	}

	// boot payload (kernel/firmware)
	if p := cfg.Payload; p != nil {
		if p.Kernel != "" {
			args = append(args, "--kernel", p.Kernel)
		}
		if p.Firmware != "" {
			args = append(args, "--firmware", p.Firmware)
		}
		if p.Initramfs != "" {
			args = append(args, "--initramfs", p.Initramfs)
		}
		if p.Cmdline != "" {
			args = append(args, "--cmdline", p.Cmdline)
		}
	}

	// --rng
	args = append(args, "--rng", fmt.Sprintf("src=%s", cfg.RNG.Src))

	// --watchdog
	if cfg.Watchdog {
		args = append(args, "--watchdog")
	}

	// --balloon
	if b := cfg.Balloon; b != nil {
		args = append(args, "--balloon", balloonToCLIArg(b))
	}

	// --serial / --console
	args = append(args, "--serial", serialToCLIArg(cfg.Serial))
	args = append(args, "--console", strings.ToLower(cfg.Console.Mode))

	return args
}

func diskToCLIArg(d chDisk) string {
	parts := []string{"path=" + d.Path}
	if d.ReadOnly {
		parts = append(parts, "readonly=on")
	}
	if d.Direct {
		parts = append(parts, "direct=on")
	}
	if d.Sparse {
		parts = append(parts, "sparse=on")
	}
	if d.ImageType != "" {
		parts = append(parts, "image_type="+strings.ToLower(d.ImageType))
	}
	if d.BackingFiles {
		parts = append(parts, "backing_files=on")
	}
	if d.NumQueues > 0 {
		parts = append(parts, fmt.Sprintf("num_queues=%d", d.NumQueues))
	}
	if d.QueueSize > 0 {
		parts = append(parts, fmt.Sprintf("queue_size=%d", d.QueueSize))
	}
	if d.Serial != "" {
		parts = append(parts, "serial="+d.Serial)
	}
	return strings.Join(parts, ",")
}

func balloonToCLIArg(b *chBalloon) string {
	parts := []string{fmt.Sprintf("size=%d", b.Size)}
	if b.DeflateOnOOM {
		parts = append(parts, "deflate_on_oom=on")
	}
	if b.FreePageReporting {
		parts = append(parts, "free_page_reporting=on")
	}
	return strings.Join(parts, ",")
}

func serialToCLIArg(s chSerial) string {
	switch strings.ToLower(s.Mode) {
	case "file":
		return "file=" + s.File
	case "tty":
		return "tty"
	default:
		return strings.ToLower(s.Mode)
	}
}
