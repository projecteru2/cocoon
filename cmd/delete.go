package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/projecteru2/core/log"
)

var deleteCmd = &cobra.Command{
	Use:   "delete ID [ID...]",
	Short: "Delete locally stored image(s)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runDelete,
}

func runDelete(_ *cobra.Command, args []string) error {
	ctx := context.Background()
	logger := log.WithFunc("cmd.delete")
	backends, _, _, err := initImageBackends(ctx)
	if err != nil {
		return err
	}

	var allDeleted []string
	for _, b := range backends {
		deleted, err := b.Delete(ctx, args)
		if err != nil {
			return fmt.Errorf("delete %s: %w", b.Type(), err)
		}
		allDeleted = append(allDeleted, deleted...)
	}
	for _, ref := range allDeleted {
		logger.Infof(ctx, "deleted: %s", ref)
	}
	if len(allDeleted) == 0 {
		logger.Infof(ctx, "no matching images found")
	}
	return nil
}
