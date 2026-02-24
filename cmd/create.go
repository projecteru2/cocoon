package cmd

import (
	"fmt"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"
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
	ctx := commandContext(cmd)
	backends, hyper, err := initBackends(ctx)
	if err != nil {
		return err
	}
	image := args[0]

	vmCfg, err := vmConfigFromFlags(cmd, image)
	if err != nil {
		return err
	}

	storageConfigs, bootCfg, err := resolveImage(ctx, backends, vmCfg)
	if err != nil {
		return err
	}

	ensureFirmwarePath(bootCfg)

	info, err := hyper.Create(ctx, vmCfg, storageConfigs, bootCfg)
	if err != nil {
		return fmt.Errorf("create VM: %w", err)
	}

	logger := log.WithFunc("cmd.create")
	logger.Infof(ctx, "VM created: %s (name: %s, state: %s)", info.ID, info.Config.Name, info.State)
	logger.Infof(ctx, "start with: cocoon start %s", info.ID)
	return nil
}
