package vm

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cmdcore "github.com/cocoonstack/cocoon/cmd/core"
	"github.com/cocoonstack/cocoon/hypervisor/cloudhypervisor"
	"github.com/cocoonstack/cocoon/types"
)

func (h Handler) Debug(cmd *cobra.Command, args []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	if conf.UseFirecracker {
		return fmt.Errorf("--fc is not supported with vm debug (debug generates Cloud Hypervisor commands)")
	}
	backends, err := cmdcore.InitImageBackends(ctx, conf)
	if err != nil {
		return err
	}

	vmCfg, err := cmdcore.VMConfigFromFlags(cmd, args[0])
	if err != nil {
		return err
	}

	maxCPU, _ := cmd.Flags().GetInt("max-cpu")
	balloon, _ := cmd.Flags().GetInt("balloon")
	cowPath, _ := cmd.Flags().GetString("cow")
	chBin, _ := cmd.Flags().GetString("ch")

	storageConfigs, boot, err := cmdcore.ResolveImage(ctx, backends, vmCfg)
	if err != nil {
		return err
	}

	memoryMB := int(vmCfg.Memory >> 20)   //nolint:mnd
	cowSizeGB := int(vmCfg.Storage >> 30) //nolint:mnd
	if balloon == 0 {
		balloon = memoryMB / 2 //nolint:mnd
	}

	printDebugRun(storageConfigs, boot, vmCfg.Name, vmCfg.Image, cowPath, chBin, vmCfg.CPU, maxCPU, memoryMB, balloon, cowSizeGB, vmCfg.Windows, boot.KernelPath != "")
	return nil
}

func printDebugRun(configs []*types.StorageConfig, boot *types.BootConfig, vmName, image, cowPath, chBin string, cpu, maxCPU, memory, balloon, cowSize int, windows, directBoot bool) {
	if directBoot {
		if cowPath == "" {
			cowPath = fmt.Sprintf("cow-%s.raw", vmName)
		}

		debugConfigs := append(append([]*types.StorageConfig(nil), configs...),
			&types.StorageConfig{Path: cowPath, RO: false, Serial: cloudhypervisor.CowSerial})
		diskArgs := cloudhypervisor.DebugDiskCLIArgs(debugConfigs, cpu)

		cocoonLayers := strings.Join(cloudhypervisor.ReverseLayerSerials(configs), ",")
		cmdline := fmt.Sprintf(
			"console=hvc0 loglevel=3 boot=cocoon-overlay cocoon.layers=%s cocoon.cow=%s clocksource=kvm-clock rw",
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
	} else {
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
		diskArgs := cloudhypervisor.DebugDiskCLIArgs([]*types.StorageConfig{{Path: cowPath, RO: false}}, cpu)
		fmt.Printf("    \"%s\" \\\n", diskArgs[0])
	}
	printCommonCHArgs(cpu, maxCPU, memory, balloon, windows)
}

func printCommonCHArgs(cpu, maxCPU, memory, balloon int, windows bool) {
	cpuExtra := ""
	if windows {
		cpuExtra = ",kvm_hyperv=on"
	}
	fmt.Printf("  --cpus boot=%d,max=%d%s \\\n", cpu, maxCPU, cpuExtra)
	fmt.Printf("  --memory size=%dM \\\n", memory)
	fmt.Printf("  --rng src=/dev/urandom \\\n")
	fmt.Printf("  --balloon size=%dM,deflate_on_oom=on,free_page_reporting=on \\\n", balloon)
	fmt.Printf("  --watchdog \\\n")
	fmt.Printf("  --serial tty --console off\n")
}
