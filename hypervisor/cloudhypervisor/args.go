package cloudhypervisor

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

const (
	// defaultDiskQueueSize is the virtio-blk queue depth per device.
	// 256 matches the Cloud Hypervisor default and provides good throughput
	// without excessive memory use per disk.
	defaultDiskQueueSize = 256
	// defaultBalloon is the memory divisor for balloon sizing: mem/defaultBalloon
	// gives the initial balloon size (25% of total memory). The balloon starts
	// inflated to 75%, allowing OOM deflation headroom.
	defaultBalloon = 4
	cidataFile     = "cidata.img"
)

func buildVMConfig(ctx context.Context, rec *hypervisor.VMRecord, consoleSockPath string) *chVMConfig {
	cpu := rec.Config.CPU
	mem := rec.Config.Memory

	maxVCPUs := runtime.NumCPU()
	if cpu > maxVCPUs {
		log.WithFunc("cloudhypervisor.buildVMConfig").Warnf(ctx,
			"requested %d vCPUs exceeds host cores (%d), clamping to %d", cpu, maxVCPUs, maxVCPUs)
		cpu = maxVCPUs
	}

	cfg := &chVMConfig{
		CPUs:     chCPUs{BootVCPUs: cpu, MaxVCPUs: maxVCPUs},
		Memory:   chMemory{Size: mem, HugePages: utils.DetectHugePages()},
		RNG:      chRNG{Src: "/dev/urandom"},
		Watchdog: true,
	}

	if isDirectBoot(rec.BootConfig) {
		cfg.Serial = &chRuntimeFile{Mode: "Off"}
		cfg.Console = &chRuntimeFile{Mode: "Pty"}
	} else {
		cfg.Serial = &chRuntimeFile{Mode: "Socket", Socket: consoleSockPath}
		cfg.Console = &chRuntimeFile{Mode: "Off"}
	}

	// Balloon: 25% of memory, only when memory >= 256 MiB.
	if mem >= minBalloonMemory {
		cfg.Balloon = &chBalloon{
			Size:              mem / defaultBalloon, //nolint:mnd
			DeflateOnOOM:      true,
			FreePageReporting: true,
		}
	}

	for _, storageConfig := range rec.StorageConfigs {
		if rec.FirstBooted && !isDirectBoot(rec.BootConfig) && isCidataDisk(storageConfig) {
			continue
		}
		cfg.Disks = append(cfg.Disks, storageConfigToDisk(storageConfig, cpu))
	}

	for _, nc := range rec.NetworkConfigs {
		cfg.Nets = append(cfg.Nets, networkConfigToNet(nc))
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

func networkConfigToNet(nc *types.NetworkConfig) chNet {
	return chNet{
		Tap:         nc.Tap,
		Mac:         nc.Mac,
		NumQueues:   nc.NumQueues,
		QueueSize:   nc.QueueSize,
		OffloadTSO:  true,
		OffloadUFO:  true,
		OffloadCsum: true,
	}
}

func storageConfigToDisk(storageConfig *types.StorageConfig, cpuCount int) chDisk {
	noDirectIO := false // use page cache, not direct I/O

	d := chDisk{
		Path:      storageConfig.Path,
		ReadOnly:  storageConfig.RO,
		Serial:    storageConfig.Serial,
		NumQueues: cpuCount,
		QueueSize: defaultDiskQueueSize,
	}

	switch {
	case filepath.Ext(storageConfig.Path) == ".qcow2":
		// cloudimg qcow2 overlay
		d.ImageType = "Qcow2"
		d.BackingFiles = !storageConfig.RO
		d.IoUring = !storageConfig.RO && runtime.GOARCH != "arm64"
	case storageConfig.RO:
		// OCI EROFS layer: readonly, leverage host page cache
		d.ImageType = "Raw"
		d.DirectIO = &noDirectIO
	default:
		// OCI COW raw: writable, leverage host page cache, sparse
		d.ImageType = "Raw"
		d.DirectIO = &noDirectIO
		d.IoUring = runtime.GOARCH != "arm64"
		d.Sparse = true
	}
	return d
}

// buildCLIArgs converts a chVMConfig into cloud-hypervisor CLI arguments.
// The resulting args include --api-socket so the socket remains available
// for later control operations (stop, shutdown, power-button).
func buildCLIArgs(cfg *chVMConfig, socketPath string) []string {
	args := []string{"--api-socket", socketPath}

	args = append(args, "--cpus", fmt.Sprintf("boot=%d,max=%d", cfg.CPUs.BootVCPUs, cfg.CPUs.MaxVCPUs))

	mem := fmt.Sprintf("size=%d", cfg.Memory.Size)
	if cfg.Memory.HugePages {
		mem += ",hugepages=on"
	}
	args = append(args, "--memory", mem)

	if len(cfg.Disks) > 0 {
		args = append(args, "--disk")
		for _, d := range cfg.Disks {
			args = append(args, diskToCLIArg(d))
		}
	}

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

	if len(cfg.Nets) > 0 {
		args = append(args, "--net")
		for _, n := range cfg.Nets {
			args = append(args, netToCLIArg(n))
		}
	}

	args = append(args, "--rng", fmt.Sprintf("src=%s", cfg.RNG.Src))

	if cfg.Watchdog {
		args = append(args, "--watchdog")
	}

	if b := cfg.Balloon; b != nil {
		args = append(args, "--balloon", balloonToCLIArg(b))
	}

	if cfg.Serial != nil {
		args = append(args, "--serial", runtimeFiletoCLIArg(cfg.Serial))
	}
	if cfg.Console != nil {
		args = append(args, "--console", runtimeFiletoCLIArg(cfg.Console))
	}

	return args
}

// kvBuilder accumulates key=value pairs for CH CLI arguments.
type kvBuilder []string

func (b *kvBuilder) add(kv string) { *b = append(*b, kv) }
func (b *kvBuilder) addIf(cond bool, kv string) {
	if cond {
		*b = append(*b, kv)
	}
}
func (b kvBuilder) String() string { return strings.Join(b, ",") }

func diskToCLIArg(d chDisk) string {
	var b kvBuilder
	b.add("path=" + d.Path)
	b.addIf(d.ReadOnly, "readonly=on")
	b.addIf(d.DirectIO != nil && !*d.DirectIO, "direct=off")
	b.addIf(d.IoUring, "io_uring=on")
	b.addIf(d.Sparse, "sparse=on")
	b.addIf(d.ImageType != "", "image_type="+strings.ToLower(d.ImageType))
	b.addIf(d.BackingFiles, "backing_files=on")
	b.addIf(d.NumQueues > 0, fmt.Sprintf("num_queues=%d", d.NumQueues))
	b.addIf(d.QueueSize > 0, fmt.Sprintf("queue_size=%d", d.QueueSize))
	b.addIf(d.Serial != "", "serial="+d.Serial)
	return b.String()
}

func netToCLIArg(n chNet) string {
	var b kvBuilder
	b.add("tap=" + n.Tap)
	b.addIf(n.Mac != "", "mac="+n.Mac)
	b.addIf(n.NumQueues > 0, fmt.Sprintf("num_queues=%d", n.NumQueues))
	b.addIf(n.QueueSize > 0, fmt.Sprintf("queue_size=%d", n.QueueSize))
	b.addIf(n.OffloadTSO, "offload_tso=on")
	b.addIf(n.OffloadUFO, "offload_ufo=on")
	b.addIf(n.OffloadCsum, "offload_csum=on")
	return b.String()
}

func balloonToCLIArg(b *chBalloon) string {
	var args kvBuilder
	args.add(fmt.Sprintf("size=%d", b.Size))
	args.addIf(b.DeflateOnOOM, "deflate_on_oom=on")
	args.addIf(b.FreePageReporting, "free_page_reporting=on")
	return args.String()
}

func runtimeFiletoCLIArg(c *chRuntimeFile) string {
	switch strings.ToLower(c.Mode) {
	case "file":
		return "file=" + c.File
	case "socket":
		return "socket=" + c.Socket
	case "tty":
		return "tty"
	default:
		return strings.ToLower(c.Mode) // "off", "null", "pty"
	}
}

// isCidataDisk reports whether a storage config is the cloud-init cidata disk.
func isCidataDisk(sc *types.StorageConfig) bool {
	return filepath.Base(sc.Path) == cidataFile
}
