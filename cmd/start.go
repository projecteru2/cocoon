package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/projecteru2/core/log"
)

var startCmd = &cobra.Command{
	Use:   "start VM [VM...]",
	Short: "Start created/stopped VM(s)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runStart,
}

func runStart(cmd *cobra.Command, args []string) error {
	ctx := commandContext(cmd)
	hyper, err := initHypervisor()
	if err != nil {
		return err
	}
	return batchVMCmd(ctx, "start", "started", hyper.Start, args)
}

// batchVMCmd is a generic handler for start/stop style commands that operate
// on a list of VM refs and report per-ID results.
func batchVMCmd(ctx context.Context, name, pastTense string, fn func(context.Context, []string) ([]string, error), refs []string) error {
	logger := log.WithFunc("cmd." + name)
	done, err := fn(ctx, refs)
	for _, id := range done {
		logger.Infof(ctx, "%s: %s", pastTense, id)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if len(done) == 0 {
		logger.Infof(ctx, "no VMs %s", strings.ToLower(pastTense))
	}
	return nil
}
