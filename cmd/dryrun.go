package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/projecteru2/cocoon/hypervisor/cloudhypervisor"
	"github.com/projecteru2/cocoon/types"
)

var dryrunCmd = func() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dryrun [flags] IMAGE",
		Short: "Generate cloud-hypervisor launch command (dry run)",
		Args:  cobra.ExactArgs(1),
		RunE:  runDryrun,
	}
	cmd.Flags().String("name", "cocoon-vm", "VM name")
	cmd.Flags().Int("cpu", 2, "boot CPUs")              //nolint:mnd
	cmd.Flags().Int("max-cpu", 8, "max CPUs")           //nolint:mnd
	cmd.Flags().Int("memory", 1024, "memory in MB")     //nolint:mnd
	cmd.Flags().Int("balloon", 0, "balloon size in MB") //nolint:mnd
	cmd.Flags().Int("storage", 10, "COW disk size in GB")
	cmd.Flags().String("cow", "", "COW disk path")
	cmd.Flags().String("ch", "cloud-hypervisor", "cloud-hypervisor binary path")
	return cmd
}()

func runDryrun(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	backends, _, _, err := initImageBackends(ctx)
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

	storageConfigs, boot, err := resolveImage(ctx, backends, vmCfg)
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
