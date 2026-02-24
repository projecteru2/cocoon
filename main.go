package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	units "github.com/docker/go-units"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/hypervisor/cloudhypervisor"
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/images/cloudimg"
	"github.com/projecteru2/cocoon/images/oci"
	"github.com/projecteru2/cocoon/progress"
	cloudimgProgress "github.com/projecteru2/cocoon/progress/cloudimg"
	ociProgress "github.com/projecteru2/cocoon/progress/oci"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

func main() {
	conf := config.DefaultConfig()
	if root := os.Getenv("COCOON_ROOT"); root != "" {
		conf.RootDir = root
		conf.RunDir = root + "/run"
		conf.LogDir = root + "/log"
	}

	if len(os.Args) < 2 {
		usage()
	}

	ctx := context.Background()

	ociStore, err := oci.New(ctx, conf)
	if err != nil {
		fatalf("init oci backend: %v", err)
	}
	cloudimgStore, err := cloudimg.New(ctx, conf)
	if err != nil {
		fatalf("init cloudimg backend: %v", err)
	}
	backends := []images.Images{ociStore, cloudimgStore}

	ch, err := cloudhypervisor.New(conf)
	if err != nil {
		fatalf("init hypervisor: %v", err)
	}

	switch os.Args[1] {
	// Image commands.
	case "pull":
		cmdPull(ctx, ociStore, cloudimgStore, os.Args[2:])
	case "list", "ls":
		cmdList(ctx, backends)
	case "run":
		cmdRun(ctx, conf, backends, os.Args[2:])
	case "delete":
		cmdDelete(ctx, backends, os.Args[2:])
	case "gc":
		cmdGC(ctx, backends, ch)

	// VM commands.
	case "create":
		cmdCreate(ctx, conf, backends, ch, os.Args[2:])
	case "start":
		batchVMCmd(ctx, "start", "Started", ch.Start, os.Args[2:])
	case "stop":
		batchVMCmd(ctx, "stop", "Stopped", ch.Stop, os.Args[2:])
	case "ps":
		cmdPS(ctx, ch)
	case "rm":
		cmdRM(ctx, ch, os.Args[2:])

	default:
		fatalf("unknown command: %s", os.Args[1])
	}
}

// ─── Image commands ──────────────────────────────────────────────────────────

// isURL returns true if the image reference looks like an HTTP(S) URL.
func isURL(ref string) bool {
	return strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
}

func cmdPull(ctx context.Context, ociStore *oci.OCI, cloudimgStore *cloudimg.CloudImg, args []string) {
	if len(args) == 0 {
		fatalf("usage: cocoon pull <image|url> [image|url...]")
	}
	for _, image := range args {
		if isURL(image) {
			pullCloudimg(ctx, cloudimgStore, image)
		} else {
			pullOCI(ctx, ociStore, image)
		}
	}
}

func pullOCI(ctx context.Context, store *oci.OCI, image string) {
	tracker := progress.NewTracker(func(e ociProgress.Event) {
		switch e.Phase {
		case ociProgress.PhasePull:
			fmt.Printf("Pulling OCI image %s (%d layers)\n", image, e.Total)
		case ociProgress.PhaseLayer:
			fmt.Printf("  [%d/%d] %s done\n", e.Index+1, e.Total, e.Digest)
		case ociProgress.PhaseCommit:
			fmt.Printf("Committing...\n")
		case ociProgress.PhaseDone:
			fmt.Printf("Done: %s\n", image)
		}
	})
	if err := store.Pull(ctx, image, tracker); err != nil {
		fatalf("pull %s: %v", image, err)
	}
}

func pullCloudimg(ctx context.Context, store *cloudimg.CloudImg, url string) {
	tracker := progress.NewTracker(func(e cloudimgProgress.Event) {
		switch e.Phase {
		case cloudimgProgress.PhaseDownload:
			if e.BytesDone == 0 {
				if e.BytesTotal > 0 {
					fmt.Printf("Downloading cloud image %s (%s)\n", url, formatSize(e.BytesTotal))
				} else {
					fmt.Printf("Downloading cloud image %s\n", url)
				}
			} else if e.BytesTotal > 0 {
				pct := float64(e.BytesDone) / float64(e.BytesTotal) * 100
				fmt.Printf("\r  %s / %s (%.1f%%)", formatSize(e.BytesDone), formatSize(e.BytesTotal), pct)
			} else {
				fmt.Printf("\r  %s downloaded", formatSize(e.BytesDone))
			}
		case cloudimgProgress.PhaseConvert:
			fmt.Printf("\nConverting to qcow2...\n")
		case cloudimgProgress.PhaseCommit:
			fmt.Printf("Committing...\n")
		case cloudimgProgress.PhaseDone:
			fmt.Printf("Done: %s\n", url)
		}
	})
	if err := store.Pull(ctx, url, tracker); err != nil {
		fatalf("pull %s: %v", url, err)
	}
}

func cmdList(ctx context.Context, backends []images.Images) {
	var all []*types.Image
	for _, b := range backends {
		imgs, err := b.List(ctx)
		if err != nil {
			fatalf("list %s: %v", b.Type(), err)
		}
		all = append(all, imgs...)
	}
	if len(all) == 0 {
		fmt.Println("No images found.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tNAME\tDIGEST\tSIZE\tCREATED")
	for _, img := range all {
		digest := img.ID
		if len(digest) > 19 {
			digest = digest[:19]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			img.Type,
			img.Name,
			digest,
			formatSize(img.Size),
			img.CreatedAt.Local().Format(time.DateTime),
		)
	}
	w.Flush() //nolint:errcheck
}

func cmdRun(ctx context.Context, conf *config.Config, backends []images.Images, args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	vmName := fs.String("name", "cocoon-vm", "VM name")
	cpu := fs.Int("cpu", 2, "boot CPUs")
	maxCPU := fs.Int("max-cpu", 8, "max CPUs")
	memory := fs.Int("memory", 1024, "memory in MB")
	balloon := fs.Int("balloon", 0, "balloon size in MB (default: memory/2)")
	cowSize := fs.Int("storage", 10, "COW disk size in GB")
	cowPath := fs.String("cow", "", "COW disk path (auto-detected extension)")
	chBin := fs.String("ch", "cloud-hypervisor", "cloud-hypervisor binary path")
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() == 0 {
		fatalf("usage: cocoon run [flags] <image>")
	}
	image := fs.Arg(0)

	// Get disk configs for this VM — try each backend until one succeeds.
	vms := []*types.VMConfig{{
		Name:   *vmName,
		CPU:    *cpu,
		Memory: int64(*memory) << 20, //nolint:mnd
		Image:  image,
	}}

	var configs []*types.StorageConfig
	var boot *types.BootConfig
	var backendType string
	var backendErrs []string
	for _, b := range backends {
		confs, boots, err := b.Config(ctx, vms)
		if err != nil {
			backendErrs = append(backendErrs, fmt.Sprintf("%s: %v", b.Type(), err))
			continue
		}
		configs = confs[0]
		boot = boots[0]
		backendType = b.Type()
		break
	}
	if backendType == "" || boot == nil {
		fatalf("image %q not resolved: %s", image, strings.Join(backendErrs, "; "))
	}

	if *balloon == 0 {
		*balloon = *memory / 2
	}

	if boot.KernelPath != "" {
		cmdRunOCI(configs, boot, *vmName, image, *cowPath, *chBin, *cpu, *maxCPU, *memory, *balloon, *cowSize)
	} else {
		cmdRunCloudimg(configs, boot, *vmName, image, *cowPath, *chBin, *cpu, *maxCPU, *memory, *balloon, *cowSize)
	}
}

func cmdRunOCI(configs []*types.StorageConfig, boot *types.BootConfig, vmName, image, cowPath, chBin string, cpu, maxCPU, memory, balloon, cowSize int) {
	if cowPath == "" {
		cowPath = fmt.Sprintf("cow-%s.raw", vmName)
	}

	// Build --disk arguments: readonly erofs layers + writable raw COW.
	var diskArgs []string
	for _, d := range configs {
		diskArgs = append(diskArgs,
			fmt.Sprintf("path=%s,readonly=on,direct=on,image_type=raw,num_queues=2,queue_size=256,serial=%s", d.Path, d.Serial))
	}
	diskArgs = append(diskArgs,
		fmt.Sprintf("path=%s,readonly=off,direct=on,sparse=on,image_type=raw,num_queues=2,queue_size=256,serial=%s", cowPath, cloudhypervisor.CowSerial))

	// Reverse layer serials for overlayfs lowerdir ordering (top layer first).
	cocoonLayers := strings.Join(cloudhypervisor.ReverseLayerSerials(configs), ",")

	cmdline := fmt.Sprintf(
		"console=ttyS0 console=hvc0 loglevel=3 boot=cocoon cocoon.layers=%s cocoon.cow=%s clocksource=kvm-clock rw",
		cocoonLayers, cloudhypervisor.CowSerial)

	fmt.Println("# Prepare COW disk")
	fmt.Printf("truncate -s %dG %s\n", cowSize, cowPath)
	fmt.Printf("mkfs.ext4 -F -m 0 -q -E lazy_itable_init=1,lazy_journal_init=1,discard %s\n", cowPath)
	fmt.Println()

	fmt.Printf("# Launch VM: %s (image: %s, boot: direct kernel)\n", vmName, image)
	fmt.Printf("%s \\\n", chBin)
	fmt.Printf("  --kernel %s \\\n", boot.KernelPath)
	fmt.Printf("  --initramfs %s \\\n", boot.InitrdPath)
	fmt.Printf("  --disk")
	for _, d := range diskArgs {
		fmt.Printf(" \\\n    \"%s\"", d)
	}
	fmt.Printf(" \\\n")
	fmt.Printf("  --cmdline \"%s\" \\\n", cmdline)
	printCommonCHArgs(cpu, maxCPU, memory, balloon)
}

func cmdRunCloudimg(configs []*types.StorageConfig, boot *types.BootConfig, vmName, image, cowPath, chBin string, cpu, maxCPU, memory, balloon, cowSize int) {
	if cowPath == "" {
		cowPath = fmt.Sprintf("cow-%s.qcow2", vmName)
	}

	// Base image is the readonly qcow2 blob from StorageConfig.
	basePath := configs[0].Path

	fmt.Println("# Prepare COW overlay")
	fmt.Printf("qemu-img create -f qcow2 -F qcow2 -b %s %s\n", basePath, cowPath)
	if cowSize > 0 {
		fmt.Printf("qemu-img resize %s %dG\n", cowPath, cowSize)
	}
	fmt.Println()

	fmt.Printf("# Launch VM: %s (image: %s, boot: UEFI firmware)\n", vmName, image)
	fmt.Printf("%s \\\n", chBin)
	fmt.Printf("  --firmware %s \\\n", boot.FirmwarePath)
	fmt.Printf("  --disk \\\n")
	fmt.Printf("    \"path=%s,readonly=off,direct=on,image_type=qcow2,backing_files=on,num_queues=2,queue_size=256\" \\\n", cowPath)
	printCommonCHArgs(cpu, maxCPU, memory, balloon)
}

func printCommonCHArgs(cpu, maxCPU, memory, balloon int) {
	fmt.Printf("  --cpus boot=%d,max=%d \\\n", cpu, maxCPU)
	fmt.Printf("  --memory size=%dM \\\n", memory)
	fmt.Printf("  --rng src=/dev/urandom \\\n")
	fmt.Printf("  --balloon size=%dM,deflate_on_oom=on,free_page_reporting=on \\\n", balloon)
	fmt.Printf("  --watchdog \\\n")
	fmt.Printf("  --serial tty --console off\n")
}

func cmdGC(ctx context.Context, backends []images.Images, ch hypervisor.Hypervisor) {
	o := gc.New()
	for _, b := range backends {
		b.RegisterGC(o)
	}
	ch.RegisterGC(o)
	if err := o.Run(ctx); err != nil {
		fatalf("gc: %v", err)
	}
	fmt.Println("GC completed.")
}

func cmdDelete(ctx context.Context, backends []images.Images, args []string) {
	if len(args) == 0 {
		fatalf("usage: cocoon delete <id|ref> [id|ref...]")
	}
	var allDeleted []string
	for _, b := range backends {
		deleted, err := b.Delete(ctx, args)
		if err != nil {
			fatalf("delete %s: %v", b.Type(), err)
		}
		allDeleted = append(allDeleted, deleted...)
	}
	for _, ref := range allDeleted {
		fmt.Printf("Deleted: %s\n", ref)
	}
	if len(allDeleted) == 0 {
		fmt.Println("No matching images found.")
	}
}

// ─── VM commands ─────────────────────────────────────────────────────────────

func cmdCreate(ctx context.Context, conf *config.Config, backends []images.Images, hyper hypervisor.Hypervisor, args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	vmName := fs.String("name", "", "VM name (required)")
	cpu := fs.Int("cpu", 2, "boot CPUs")
	memStr := fs.String("memory", "1G", "memory size (e.g., 512M, 1G)")
	storStr := fs.String("storage", "10G", "COW disk size (e.g., 10G, 20G)")
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() == 0 {
		fatalf("usage: cocoon create [flags] <image>")
	}
	image := fs.Arg(0)

	if *vmName == "" {
		*vmName = fmt.Sprintf("cocoon-%s", image)
	}

	memBytes, err := units.RAMInBytes(*memStr)
	if err != nil {
		fatalf("invalid --memory %q: %v", *memStr, err)
	}
	storBytes, err := units.RAMInBytes(*storStr)
	if err != nil {
		fatalf("invalid --storage %q: %v", *storStr, err)
	}

	vmCfg := &types.VMConfig{
		Name:    *vmName,
		CPU:     *cpu,
		Memory:  memBytes,
		Storage: storBytes,
		Image:   image,
	}

	// Resolve image → StorageConfigs + BootConfig from image backends.
	vms := []*types.VMConfig{vmCfg}
	var storageConfigs []*types.StorageConfig
	var bootCfg *types.BootConfig
	var backendErrs []string
	for _, b := range backends {
		confs, boots, bErr := b.Config(ctx, vms)
		if bErr != nil {
			backendErrs = append(backendErrs, fmt.Sprintf("%s: %v", b.Type(), bErr))
			continue
		}
		storageConfigs = confs[0]
		bootCfg = boots[0]
		break
	}
	if bootCfg == nil {
		fatalf("image %q not resolved: %s", image, strings.Join(backendErrs, "; "))
	}

	// If cloudimg, set firmware path from global config.
	if bootCfg.KernelPath == "" && bootCfg.FirmwarePath == "" {
		bootCfg.FirmwarePath = conf.FirmwarePath()
	}

	info, err := hyper.Create(ctx, vmCfg, storageConfigs, bootCfg)
	if err != nil {
		fatalf("create VM: %v", err)
	}

	fmt.Printf("VM created: %s (name: %s, state: %s)\n", info.ID, info.Config.Name, info.State)
	fmt.Printf("Start with: cocoon start %s\n", info.ID)
}

// batchVMCmd is a generic handler for start/stop style commands that operate
// on a list of VM IDs and report per-ID results.
func batchVMCmd(ctx context.Context, name, pastTense string, fn func(context.Context, []string) ([]string, error), args []string) {
	if len(args) == 0 {
		fatalf("usage: cocoon %s <vm-id> [vm-id...]", name)
	}
	done, err := fn(ctx, args)
	for _, id := range done {
		fmt.Printf("%s: %s\n", pastTense, id)
	}
	if err != nil {
		fatalf("%s: %v", name, err)
	}
	if len(done) == 0 {
		fmt.Printf("No VMs %s.\n", strings.ToLower(pastTense))
	}
}

func cmdPS(ctx context.Context, hyper hypervisor.Hypervisor) {
	vms, err := hyper.List(ctx)
	if err != nil {
		fatalf("ps: %v", err)
	}
	if len(vms) == 0 {
		fmt.Println("No VMs found.")
		return
	}

	// Sort by creation time (oldest first).
	sort.Slice(vms, func(i, j int) bool { return vms[i].CreatedAt.Before(vms[j].CreatedAt) })

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATE\tCPU\tMEMORY\tIMAGE\tCREATED")
	for _, vm := range vms {
		state := reconcileState(vm)
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			truncateID(vm.ID, 12),
			vm.Config.Name,
			state,
			vm.Config.CPU,
			units.BytesSize(float64(vm.Config.Memory)),
			vm.Config.Image,
			vm.CreatedAt.Local().Format(time.DateTime),
		)
	}
	w.Flush() //nolint:errcheck
}

// reconcileState checks actual process liveness to detect stale "running" records.
func reconcileState(vm *types.VMInfo) string {
	if vm.State == types.VMStateRunning && !utils.IsProcessAlive(vm.PID) {
		return "stopped (stale)"
	}
	return string(vm.State)
}

func cmdRM(ctx context.Context, hyper hypervisor.Hypervisor, args []string) {
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	force := fs.Bool("force", false, "force delete running VMs")
	fs.Parse(args) //nolint:errcheck

	ids := fs.Args()
	if len(ids) == 0 {
		fatalf("usage: cocoon rm [--force] <vm-id> [vm-id...]")
	}

	deleted, err := hyper.Delete(ctx, ids, *force)
	for _, id := range deleted {
		fmt.Printf("Deleted VM: %s\n", id)
	}
	if err != nil {
		fatalf("rm: %v", err)
	}
	if len(deleted) == 0 {
		fmt.Println("No VMs deleted.")
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func formatSize(bytes int64) string {
	return units.HumanSize(float64(bytes))
}

func truncateID(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}

func usage() {
	fmt.Fprintf(os.Stderr, `Cocoon - MicroVM Engine

Usage: cocoon <command> [arguments]

Environment:
  COCOON_ROOT    Root data directory (default: /var/lib/cocoon)

Image Commands:
  pull <image|url> [...]          Pull OCI image(s) or cloud image URL(s)
  list                            List locally stored images (all backends)
  run  [flags] <image>            Generate cloud-hypervisor launch command (dry run)
  delete <id|ref> [id|ref...]     Delete locally stored image(s)
  gc                              Remove unreferenced blobs, boot files, and VM dirs

VM Commands:
  create [flags] <image>          Create a VM (prepare COW, register in DB)
  start  <vm-id> [vm-id...]       Start created/stopped VM(s)
  stop   <vm-id> [vm-id...]       Stop running VM(s)
  ps                              List VMs with status
  rm     [--force] <vm-id> [...]  Delete VM(s) (--force to stop running VMs first)

Create flags:
  -name      VM name (default: cocoon-<image>)
  -cpu       Boot CPUs (default: 2)
  -memory    Memory size (default: 1G, e.g. 512M, 2G)
  -storage   COW disk size (default: 10G)

Pull:
  OCI images:    cocoon pull ubuntu:24.04
  Cloud images:  cocoon pull https://cloud-images.ubuntu.com/.../ubuntu-24.04.qcow2
`)
	os.Exit(1)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
