package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect VM",
	Short: "Show detailed VM info (JSON)",
	Args:  cobra.ExactArgs(1),
	RunE:  runInspect,
}

func runInspect(_ *cobra.Command, args []string) error {
	ctx := context.Background()
	hyper, err := initHypervisor()
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
