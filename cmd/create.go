package cmd

import (
	"context"
	"fmt"

	units "github.com/docker/go-units"
	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	"github.com/projecteru2/cocoon/types"
)

var createCmd = func() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [flags] IMAGE",
		Short: "Create a VM from an image",
		Args:  cobra.ExactArgs(1),
		RunE:  runCreate,
	}
	cmd.Flags().String("name", "", "VM name")
	cmd.Flags().Int("cpu", 2, "boot CPUs")                //nolint:mnd
	cmd.Flags().String("memory", "1G", "memory size")     //nolint:mnd
	cmd.Flags().String("storage", "10G", "COW disk size") //nolint:mnd
	return cmd
}()

func runCreate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
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

	// If cloudimg, set firmware path from global config.
	if bootCfg.KernelPath == "" && bootCfg.FirmwarePath == "" {
		bootCfg.FirmwarePath = conf.FirmwarePath()
	}

	info, err := hyper.Create(ctx, vmCfg, storageConfigs, bootCfg)
	if err != nil {
		return fmt.Errorf("create VM: %w", err)
	}

	logger := log.WithFunc("cmd.create")
	logger.Infof(ctx, "VM created: %s (name: %s, state: %s)", info.ID, info.Config.Name, info.State)
	logger.Infof(ctx, "start with: cocoon start %s", info.ID)
	return nil
}
