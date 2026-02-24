package cmd

import (
	"context"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	"github.com/projecteru2/cocoon/gc"
)

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Remove unreferenced blobs, boot files, and VM dirs",
	RunE:  runGC,
}

func runGC(_ *cobra.Command, _ []string) error {
	ctx := context.Background()
	backends, hyper, err := initBackends(ctx)
	if err != nil {
		return err
	}

	o := gc.New()
	for _, b := range backends {
		b.RegisterGC(o)
	}
	hyper.RegisterGC(o)
	if err := o.Run(ctx); err != nil {
		return err
	}
	log.WithFunc("cmd.gc").Infof(ctx, "GC completed")
	return nil
}
