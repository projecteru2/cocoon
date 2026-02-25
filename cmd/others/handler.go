package others

import (
	"fmt"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	cmdcore "github.com/projecteru2/cocoon/cmd/core"
	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/version"
)

type Handler struct {
	cmdcore.BaseHandler
}

func (h Handler) GC(cmd *cobra.Command, _ []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	backends, hyper, err := cmdcore.InitBackends(ctx, conf)
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

func (h Handler) Version(_ *cobra.Command, _ []string) error {
	fmt.Print(version.String())
	return nil
}
