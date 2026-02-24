package cmd

import (
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop VM [VM...]",
	Short: "Stop running VM(s)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runStop,
}

func runStop(cmd *cobra.Command, args []string) error {
	ctx := commandContext(cmd)
	hyper, err := initHypervisor()
	if err != nil {
		return err
	}
	return batchVMCmd(ctx, "stop", "stopped", hyper.Stop, args)
}
