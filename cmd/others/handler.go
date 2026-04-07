package others

import (
	"fmt"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	cmdcore "github.com/cocoonstack/cocoon/cmd/core"
	"github.com/cocoonstack/cocoon/gc"
	"github.com/cocoonstack/cocoon/version"
)

type Handler struct {
	cmdcore.BaseHandler
}

func (h Handler) GC(cmd *cobra.Command, _ []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	backends, err := cmdcore.InitImageBackends(ctx, conf)
	if err != nil {
		return err
	}
	netProvider, err := cmdcore.InitNetwork(conf)
	if err != nil {
		return err
	}
	snapBackend, err := cmdcore.InitSnapshot(conf)
	if err != nil {
		return err
	}

	o := gc.New()
	for _, b := range backends {
		b.RegisterGC(o)
	}
	// Register ALL hypervisor backends so GC protects blobs from both CH and FC VMs.
	for _, hyper := range cmdcore.InitAllHypervisors(conf) {
		hyper.RegisterGC(o)
	}
	netProvider.RegisterGC(o)
	snapBackend.RegisterGC(o)
	if err := o.Run(ctx); err != nil {
		return err
	}
	log.WithFunc("cmd.gc").Info(ctx, "GC completed")
	return nil
}

func (h Handler) Version(_ *cobra.Command, _ []string) error {
	fmt.Print(version.String())
	return nil
}
