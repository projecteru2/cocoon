package others

import (
	"fmt"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	cmdcore "github.com/projecteru2/cocoon/cmd/core"
	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/version"
)

type Handler struct {
	ConfProvider func() *config.Config
}

func (h Handler) conf() (*config.Config, error) {
	if h.ConfProvider == nil {
		return nil, fmt.Errorf("config provider is nil")
	}
	conf := h.ConfProvider()
	if conf == nil {
		return nil, fmt.Errorf("config not initialized")
	}
	return conf, nil
}

func (h Handler) GC(cmd *cobra.Command, _ []string) error {
	conf, err := h.conf()
	if err != nil {
		return err
	}
	ctx := cmdcore.CommandContext(cmd)
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
