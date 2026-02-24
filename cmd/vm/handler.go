package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	units "github.com/docker/go-units"
	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	cmdcore "github.com/projecteru2/cocoon/cmd/core"
	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/console"
	"github.com/projecteru2/cocoon/hypervisor/cloudhypervisor"
	"github.com/projecteru2/cocoon/types"
)

type Handler struct {
	ConfProvider func() *config.Config
}

func (h Handler) conf() (*config.Config, error) {
	if h.ConfProvider == nil {
		return nil, fmt.Errorf("config provider is nil")
	}
	conf := h.ConfProvider()
	if conf == nil {
		return nil, fmt.Errorf("config not initialized")
	}
	return conf, nil
}

func (h Handler) Create(cmd *cobra.Command, args []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
	backends, hyper, err := cmdcore.InitBackends(ctx, conf)
	if err != nil {
		return err
	}
	image := args[0]

	vmCfg, err := cmdcore.VMConfigFromFlags(cmd, image)
	if err != nil {
		return err
	}

	storageConfigs, bootCfg, err := cmdcore.ResolveImage(ctx, backends, vmCfg)
	if err != nil {
		return err
	}
	cmdcore.EnsureFirmwarePath(conf, bootCfg)

	info, err := hyper.Create(ctx, vmCfg, storageConfigs, bootCfg)
	if err != nil {
		return fmt.Errorf("create VM: %w", err)
	}

	logger := log.WithFunc("cmd.create")
	logger.Infof(ctx, "VM created: %s (name: %s, state: %s)", info.ID, info.Config.Name, info.State)
	logger.Infof(ctx, "start with: cocoon start %s", info.ID)
	return nil
}

func (h Handler) Run(cmd *cobra.Command, args []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
	logger := log.WithFunc("cmd.run")
	backends, hyper, err := cmdcore.InitBackends(ctx, conf)
	if err != nil {
		return err
	}
	image := args[0]

	vmCfg, err := cmdcore.VMConfigFromFlags(cmd, image)
	if err != nil {
		return err
	}

	storageConfigs, bootCfg, err := cmdcore.ResolveImage(ctx, backends, vmCfg)
	if err != nil {
		return err
	}
	cmdcore.EnsureFirmwarePath(conf, bootCfg)

	info, err := hyper.Create(ctx, vmCfg, storageConfigs, bootCfg)
	if err != nil {
		return fmt.Errorf("create VM: %w", err)
	}
	logger.Infof(ctx, "VM created: %s (name: %s)", info.ID, info.Config.Name)

	started, err := hyper.Start(ctx, []string{info.ID})
	if err != nil {
		return fmt.Errorf("start VM %s: %w", info.ID, err)
	}
	for _, id := range started {
		logger.Infof(ctx, "started: %s", id)
	}
	return nil
}

func (h Handler) Start(cmd *cobra.Command, args []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}
	return batchVMCmd(ctx, "start", "started", hyper.Start, args)
}

func (h Handler) Stop(cmd *cobra.Command, args []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}
	return batchVMCmd(ctx, "stop", "stopped", hyper.Stop, args)
}

func (h Handler) PS(cmd *cobra.Command, _ []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}

	vms, err := hyper.List(ctx)
	if err != nil {
		return fmt.Errorf("ps: %w", err)
	}
	if len(vms) == 0 {
		fmt.Println("No VMs found.")
		return nil
	}

	sort.Slice(vms, func(i, j int) bool { return vms[i].CreatedAt.Before(vms[j].CreatedAt) })

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tSTATE\tCPU\tMEMORY\tIMAGE\tCREATED")
	for _, vm := range vms {
		state := cmdcore.ReconcileState(vm)
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			vm.ID,
			vm.Config.Name,
			state,
			vm.Config.CPU,
			units.BytesSize(float64(vm.Config.Memory)),
			vm.Config.Image,
			vm.CreatedAt.Local().Format(time.DateTime),
		)
	}
	w.Flush() //nolint:errcheck,gosec
	return nil
}

func (h Handler) Inspect(cmd *cobra.Command, args []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}

	info, err := hyper.Inspect(ctx, args[0])
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(info)
	return nil
}

func (h Handler) Console(cmd *cobra.Command, args []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}
	ref := args[0]

	ptyPath, err := hyper.Console(ctx, ref)
	if err != nil {
		return fmt.Errorf("console: %w", err)
	}

	pty, err := os.OpenFile(ptyPath, os.O_RDWR, 0) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open PTY %s: %w", ptyPath, err)
	}
	defer pty.Close() //nolint:errcheck

	escapeStr, _ := cmd.Flags().GetString("escape-char")
	escapeChar, err := console.ParseEscapeChar(escapeStr)
	if err != nil {
		return err
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer func() {
		_ = term.Restore(fd, oldState)
		fmt.Fprintf(os.Stderr, "\r\nDisconnected from %s.\r\n", ref)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
		}
	}()

	cleanupWinch := console.HandleSIGWINCH(os.Stdin, pty)
	defer cleanupWinch()

	escapeDisplay := console.FormatEscapeChar(escapeChar)
	fmt.Fprintf(os.Stderr, "Connected to %s (escape sequence: %s.)\r\n", ref, escapeDisplay)

	if err := console.Relay(ctx, pty, escapeChar); err != nil {
		fmt.Fprintf(os.Stderr, "\r\nrelay error: %v\r\n", err)
	}
	return nil
}

func (h Handler) RM(cmd *cobra.Command, args []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
	logger := log.WithFunc("cmd.rm")
	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}

	force, _ := cmd.Flags().GetBool("force")

	deleted, err := hyper.Delete(ctx, args, force)
	for _, id := range deleted {
		logger.Infof(ctx, "deleted VM: %s", id)
	}
	if err != nil {
		return fmt.Errorf("rm: %w", err)
	}
	if len(deleted) == 0 {
		logger.Infof(ctx, "no VMs deleted")
	}
	return nil
}

func (h Handler) DryRun(cmd *cobra.Command, args []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
	backends, _, _, err := cmdcore.InitImageBackends(ctx, conf)
	if err != nil {
		return err
	}
	image := args[0]

	vmName, _ := cmd.Flags().GetString("name")
	cpu, _ := cmd.Flags().GetInt("cpu")
	maxCPU, _ := cmd.Flags().GetInt("max-cpu")
	memory, _ := cmd.Flags().GetInt("memory")
	balloon, _ := cmd.Flags().GetInt("balloon")
	cowSize, _ := cmd.Flags().GetInt("storage")
	cowPath, _ := cmd.Flags().GetString("cow")
	chBin, _ := cmd.Flags().GetString("ch")

	vmCfg := &types.VMConfig{
		Name:   vmName,
		CPU:    cpu,
		Memory: int64(memory) << 20, //nolint:mnd
		Image:  image,
	}

	storageConfigs, boot, err := cmdcore.ResolveImage(ctx, backends, vmCfg)
	if err != nil {
		return err
	}

	if balloon == 0 {
		balloon = memory / 2
	}

	if boot.KernelPath != "" {
		printRunOCI(storageConfigs, boot, vmName, image, cowPath, chBin, cpu, maxCPU, memory, balloon, cowSize)
	} else {
		printRunCloudimg(storageConfigs, boot, vmName, image, cowPath, chBin, cpu, maxCPU, memory, balloon, cowSize)
	}
	return nil
}

func batchVMCmd(ctx context.Context, name, pastTense string, fn func(context.Context, []string) ([]string, error), refs []string) error {
	logger := log.WithFunc("cmd." + name)
	done, err := fn(ctx, refs)
	for _, id := range done {
		logger.Infof(ctx, "%s: %s", pastTense, id)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if len(done) == 0 {
		logger.Infof(ctx, "no VMs %s", strings.ToLower(pastTense))
	}
	return nil
}

func printRunOCI(configs []*types.StorageConfig, boot *types.BootConfig, vmName, image, cowPath, chBin string, cpu, maxCPU, memory, balloon, cowSize int) {
	if cowPath == "" {
		cowPath = fmt.Sprintf("cow-%s.raw", vmName)
	}

	var diskArgs []string
	for _, d := range configs {
		diskArgs = append(diskArgs,
			fmt.Sprintf("path=%s,readonly=on,direct=on,image_type=raw,num_queues=2,queue_size=256,serial=%s", d.Path, d.Serial))
	}
	diskArgs = append(diskArgs,
		fmt.Sprintf("path=%s,readonly=off,direct=on,sparse=on,image_type=raw,num_queues=2,queue_size=256,serial=%s", cowPath, cloudhypervisor.CowSerial))

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

func printRunCloudimg(configs []*types.StorageConfig, boot *types.BootConfig, vmName, image, cowPath, chBin string, cpu, maxCPU, memory, balloon, cowSize int) {
	if cowPath == "" {
		cowPath = fmt.Sprintf("cow-%s.qcow2", vmName)
	}

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
