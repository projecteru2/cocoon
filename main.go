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
	"github.com/projecteru2/cocoon/images/oci"
	"github.com/projecteru2/cocoon/progress"
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
	store, err := oci.New(ctx, cfg)
	if err != nil {
		fatalf("init image: %v", err)
	}

	switch os.Args[1] {
	case "pull":
		cmdPull(ctx, store, os.Args[2:])
	case "list", "ls":
		cmdList(ctx, store)
	case "run":
		cmdRun(ctx, store, os.Args[2:])
	case "delete", "rm":
		cmdDelete(ctx, store, os.Args[2:])
	case "gc":
		cmdGC(ctx, store)
	default:
		fatalf("unknown command: %s", os.Args[1])
	}
}

func cmdPull(ctx context.Context, store images.Images, args []string) {
	if len(args) == 0 {
		fatalf("usage: cocoon pull <image> [image...]")
	}
	for _, image := range args {
		tracker := progress.NewTracker(func(e ociProgress.Event) {
			switch e.Phase {
			case ociProgress.PhasePull:
				fmt.Printf("Pulling %s (%d layers)\n", image, e.Total)
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
}

func cmdList(ctx context.Context, store images.Images) {
	images, err := store.List(ctx)
	if err != nil {
		fatalf("list: %v", err)
	}
	if len(images) == 0 {
		fmt.Println("No images found.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDIGEST\tSIZE\tCREATED")
	for _, img := range images {
		digest := img.ID
		if len(digest) > 19 {
			digest = digest[:19]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			img.Name,
			digest,
			formatSize(img.Size),
			img.CreatedAt.Local().Format(time.DateTime),
		)
	}
	w.Flush() //nolint:errcheck
}

func cmdRun(ctx context.Context, store images.Images, args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	vmName := fs.String("name", "cocoon-vm", "VM name")
	cpu := fs.Int("cpu", 2, "boot CPUs")
	maxCPU := fs.Int("max-cpu", 8, "max CPUs")
	memory := fs.Int("memory", 1024, "memory in MB")
	balloon := fs.Int("balloon", 0, "balloon size in MB (default: memory/2)")
	cowSize := fs.Int("storage", 10, "COW disk size in GB")
	cowPath := fs.String("cow", "", "COW disk path (default: cow-<name>.raw)")
	chBin := fs.String("ch", "cloud-hypervisor", "cloud-hypervisor binary path")
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() == 0 {
		fatalf("usage: cocoon run [flags] <image>")
	}
	image := fs.Arg(0)

	// Get disk configs for this VM.
	vms := []*types.VMConfig{{
		Name:   *vmName,
		CPU:    *cpu,
		Memory: int64(*memory) << 20, //nolint:mnd
		Image:  image,
	}}
	configs, boots, err := store.Config(ctx, vms)
	if err != nil {
		fatalf("config: %v", err)
	}
	boot := boots[0]

	if *cowPath == "" {
		*cowPath = fmt.Sprintf("cow-%s.raw", *vmName)
	}
	if *balloon == 0 {
		*balloon = *memory / 2
	}

	disks := configs[0]

	// Build --disk arguments.
	var diskArgs []string
	var layerSerials []string
	for _, d := range disks {
		diskArgs = append(diskArgs,
			fmt.Sprintf("path=%s,readonly=on,direct=on,image_type=raw,num_queues=2,queue_size=256,serial=%s", d.Path, d.Serial))
		layerSerials = append(layerSerials, d.Serial)
	}
	// COW disk: direct=on, sparse=on for best performance.
	diskArgs = append(diskArgs,
		fmt.Sprintf("path=%s,readonly=off,direct=on,sparse=on,image_type=raw,num_queues=2,queue_size=256,serial=%s", *cowPath, cowSerial))

	// Reverse layer serials for overlayfs lowerdir ordering (top layer first).
	reversed := make([]string, len(layerSerials))
	for i, s := range layerSerials {
		reversed[len(layerSerials)-1-i] = s
	}
	cocoonLayers := strings.Join(reversed, ",")

	cmdline := fmt.Sprintf(
		"console=ttyS0 loglevel=3 boot=cocoon cocoon.layers=%s cocoon.cow=%s clocksource=kvm-clock rw",
		cocoonLayers, cowSerial)

	// Print COW preparation.
	fmt.Println("# Prepare COW disk")
	fmt.Printf("truncate -s %dG %s\n", *cowSize, *cowPath)
	fmt.Printf("mkfs.ext4 -F -m 0 -q -E lazy_itable_init=1,lazy_journal_init=1,discard %s\n", *cowPath)
	fmt.Println()

	// Print cloud-hypervisor command.
	fmt.Printf("# Launch VM: %s (image: %s)\n", *vmName, image)
	fmt.Printf("%s \\\n", *chBin)
	fmt.Printf("  --kernel %s \\\n", boot.KernelPath)
	fmt.Printf("  --initramfs %s \\\n", boot.InitrdPath)
	fmt.Printf("  --disk")
	for _, d := range diskArgs {
		fmt.Printf(" \\\n    \"%s\"", d)
	}
	fmt.Printf(" \\\n")
	fmt.Printf("  --cmdline \"%s\" \\\n", cmdline)
	fmt.Printf("  --cpus boot=%d,max=%d \\\n", *cpu, *maxCPU)
	fmt.Printf("  --memory size=%dM \\\n", *memory)
	fmt.Printf("  --rng src=/dev/urandom \\\n")
	fmt.Printf("  --balloon size=%dM,deflate_on_oom=on,free_page_reporting=on \\\n", *balloon)
	fmt.Printf("  --watchdog \\\n")
	fmt.Printf("  --serial tty --console off\n")
}

func cmdGC(ctx context.Context, store images.Images) {
	if err := store.GC(ctx); err != nil {
		fatalf("gc: %v", err)
	}
	fmt.Println("GC completed.")
}

func cmdDelete(ctx context.Context, store images.Images, args []string) {
	if len(args) == 0 {
		fatalf("usage: cocoon delete <id|ref> [id|ref...]")
	}
	deleted, err := store.Delete(ctx, args)
	for _, ref := range deleted {
		fmt.Printf("Deleted: %s\n", ref)
	}
	if err != nil {
		fatalf("delete: %v", err)
	}
	if len(deleted) == 0 {
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
	fmt.Fprintf(os.Stderr, `Cocoon - MicroVM Engine with OCI Image Support

Usage: cocoon <command> [arguments]

Environment:
  COCOON_ROOT    Root data directory (default: /var/lib/cocoon)

Commands:
  pull <image> [image...]        Pull OCI image(s) from registry
  list                           List locally stored images
  run  [flags] <image>           Generate cloud-hypervisor launch command
  delete <id|ref> [id|ref...]    Delete locally stored image(s)
  gc                             Remove unreferenced blobs and boot files

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
