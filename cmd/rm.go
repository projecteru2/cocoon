package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/projecteru2/core/log"
)

var rmCmd = func() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [flags] VM [VM...]",
		Short: "Delete VM(s) (--force to stop running VMs first)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runRM,
	}
	cmd.Flags().Bool("force", false, "force delete running VMs")
	return cmd
}()

func runRM(cmd *cobra.Command, args []string) error {
	ctx := commandContext(cmd)
	logger := log.WithFunc("cmd.rm")
	hyper, err := initHypervisor()
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
