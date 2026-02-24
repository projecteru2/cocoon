package cmd

import (
	"context"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop VM [VM...]",
	Short: "Stop running VM(s)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runStop,
}

func runStop(_ *cobra.Command, args []string) error {
	ctx := context.Background()
	hyper, err := initHypervisor()
	if err != nil {
		return err
	}
	return batchVMCmd(ctx, "stop", "stopped", hyper.Stop, args)
}
