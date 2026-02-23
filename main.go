package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/images/cloudimg"
	"github.com/projecteru2/cocoon/images/oci"
	"github.com/projecteru2/cocoon/progress"
	cloudimgProgress "github.com/projecteru2/cocoon/progress/cloudimg"
	ociProgress "github.com/projecteru2/cocoon/progress/oci"
	"github.com/projecteru2/cocoon/types"
)

const cowSerial = "cocoon-cow"

func main() {
	cfg := config.DefaultConfig()
	if root := os.Getenv("COCOON_ROOT"); root != "" {
		cfg.RootDir = root
	}

	if len(os.Args) < 2 {
		usage()
	}

	ctx := context.Background()

	ociStore, err := oci.New(ctx, cfg)
	if err != nil {
		fatalf("init oci backend: %v", err)
	}
	cloudimgStore, err := cloudimg.New(ctx, cfg)
	if err != nil {
		fatalf("init cloudimg backend: %v", err)
	}
	backends := []images.Images{ociStore, cloudimgStore}

	switch os.Args[1] {
	case "pull":
		cmdPull(ctx, ociStore, cloudimgStore, os.Args[2:])
	case "list", "ls":
		cmdList(ctx, backends)
	case "run":
		cmdRun(ctx, cfg, backends, os.Args[2:])
	case "delete", "rm":
		cmdDelete(ctx, backends, os.Args[2:])
	case "gc":
		cmdGC(ctx, backends)
	default:
		fatalf("unknown command: %s", os.Args[1])
	}
}

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

func cmdRun(ctx context.Context, cfg *config.Config, backends []images.Images, args []string) {
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
	for _, b := range backends {
		cfgs, boots, err := b.Config(ctx, vms)
		if err != nil {
			continue
		}
		configs = cfgs[0]
		boot = boots[0]
		backendType = b.Type()
		break
	}
	if backendType == "" {
		fatalf("image %q not found in any backend", image)
	}

	if *balloon == 0 {
		*balloon = *memory / 2
	}

	if boot.KernelPath != "" {
		cmdRunOCI(cfg, configs, boot, *vmName, image, *cowPath, *chBin, *cpu, *maxCPU, *memory, *balloon, *cowSize)
	} else {
		cmdRunCloudimg(cfg, configs, *vmName, image, *cowPath, *chBin, *cpu, *maxCPU, *memory, *balloon, *cowSize)
	}
}

func cmdRunOCI(cfg *config.Config, configs []*types.StorageConfig, boot *types.BootConfig, vmName, image, cowPath, chBin string, cpu, maxCPU, memory, balloon, cowSize int) {
	if cowPath == "" {
		cowPath = fmt.Sprintf("cow-%s.raw", vmName)
	}

	// Build --disk arguments: readonly erofs layers + writable raw COW.
	var diskArgs []string
	var layerSerials []string
	for _, d := range configs {
		diskArgs = append(diskArgs,
			fmt.Sprintf("path=%s,readonly=on,direct=on,image_type=raw,num_queues=2,queue_size=256,serial=%s", d.Path, d.Serial))
		layerSerials = append(layerSerials, d.Serial)
	}
	diskArgs = append(diskArgs,
		fmt.Sprintf("path=%s,readonly=off,direct=on,sparse=on,image_type=raw,num_queues=2,queue_size=256,serial=%s", cowPath, cowSerial))

	// Reverse layer serials for overlayfs lowerdir ordering (top layer first).
	reversed := make([]string, len(layerSerials))
	for i, s := range layerSerials {
		reversed[len(layerSerials)-1-i] = s
	}
	cocoonLayers := strings.Join(reversed, ",")

	cmdline := fmt.Sprintf(
		"console=ttyS0 loglevel=3 boot=cocoon cocoon.layers=%s cocoon.cow=%s clocksource=kvm-clock rw",
		cocoonLayers, cowSerial)

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

func cmdRunCloudimg(cfg *config.Config, configs []*types.StorageConfig, vmName, image, cowPath, chBin string, cpu, maxCPU, memory, balloon, cowSize int) {
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
	fmt.Printf("  --firmware %s \\\n", cfg.FirmwarePath())
	fmt.Printf("  --disk \\\n")
	fmt.Printf("    \"path=%s,readonly=off,image_type=qcow2,backing_files=on,num_queues=2,queue_size=256\" \\\n", cowPath)
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

func cmdGC(ctx context.Context, backends []images.Images) {
	for _, b := range backends {
		if err := b.GC(ctx); err != nil {
			fatalf("gc %s: %v", b.Type(), err)
		}
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

func formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Cocoon - MicroVM Engine

Usage: cocoon <command> [arguments]

Environment:
  COCOON_ROOT    Root data directory (default: /var/lib/cocoon)

Commands:
  pull <image|url> [...]          Pull OCI image(s) or cloud image URL(s)
  list                            List locally stored images (all backends)
  run  [flags] <image>            Generate cloud-hypervisor launch command
  delete <id|ref> [id|ref...]     Delete locally stored image(s)
  gc                              Remove unreferenced blobs and boot files

Pull:
  OCI images:    cocoon pull ubuntu:24.04
  Cloud images:  cocoon pull https://cloud-images.ubuntu.com/.../ubuntu-24.04.qcow2

Run flags:
  -name      VM name (default: cocoon-vm)
  -cpu       Boot CPUs (default: 2)
  -max-cpu   Max CPUs (default: 8)
  -memory    Memory in MB (default: 1024)
  -balloon   Balloon size in MB (default: memory/2)
  -storage   COW disk size in GB (default: 10)
  -cow       COW disk path (default: cow-<name>.raw)
  -ch        cloud-hypervisor binary (default: cloud-hypervisor)
`)
	os.Exit(1)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
