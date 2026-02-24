package cmd

import (
	"fmt"

	units "github.com/docker/go-units"
	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	"github.com/projecteru2/cocoon/types"
)

var runCmd = func() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] IMAGE",
		Short: "Create and start a VM from an image",
		Args:  cobra.ExactArgs(1),
		RunE:  runRun,
	}
	cmd.Flags().String("name", "", "VM name")
	cmd.Flags().Int("cpu", 2, "boot CPUs")                //nolint:mnd
	cmd.Flags().String("memory", "1G", "memory size")     //nolint:mnd
	cmd.Flags().String("storage", "10G", "COW disk size") //nolint:mnd
	return cmd
}()

func runRun(cmd *cobra.Command, args []string) error {
	ctx := commandContext(cmd)
	logger := log.WithFunc("cmd.run")
	backends, hyper, err := initBackends(ctx)
	if err != nil {
		return err
	}
	image := args[0]

	vmName, _ := cmd.Flags().GetString("name")
	cpu, _ := cmd.Flags().GetInt("cpu")
	memStr, _ := cmd.Flags().GetString("memory")
	storStr, _ := cmd.Flags().GetString("storage")

	if vmName == "" {
		vmName = fmt.Sprintf("cocoon-%s", image)
	}

	memBytes, err := units.RAMInBytes(memStr)
	if err != nil {
		return fmt.Errorf("invalid --memory %q: %w", memStr, err)
	}
	storBytes, err := units.RAMInBytes(storStr)
	if err != nil {
		return fmt.Errorf("invalid --storage %q: %w", storStr, err)
	}

	vmCfg := &types.VMConfig{
		Name:    vmName,
		CPU:     cpu,
		Memory:  memBytes,
		Storage: storBytes,
		Image:   image,
	}

	storageConfigs, bootCfg, err := resolveImage(ctx, backends, vmCfg)
	if err != nil {
		return err
	}

	if bootCfg.KernelPath == "" && bootCfg.FirmwarePath == "" {
		bootCfg.FirmwarePath = conf.FirmwarePath()
	}

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
