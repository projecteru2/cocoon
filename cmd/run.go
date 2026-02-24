package cmd

import (
	"fmt"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"
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
