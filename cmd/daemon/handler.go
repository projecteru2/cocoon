package daemon

import (
	"os"
	"path/filepath"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	cmdcore "github.com/projecteru2/cocoon/cmd/core"
	"github.com/projecteru2/cocoon/daemon"
)

// Handler implements Actions.
type Handler struct {
	cmdcore.BaseHandler
}

func (h Handler) Start(cmd *cobra.Command, _ []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}

	svc, err := cmdcore.InitService(cmd, conf)
	if err != nil {
		return err
	}

	listen, _ := cmd.Flags().GetString("listen")
	if listen == "" {
		listen = filepath.Join(conf.RunDir, "cocoon.sock")
	}

	d, err := daemon.New(svc, listen)
	if err != nil {
		return err
	}

	logger := log.WithFunc("cmd.daemon")
	logger.Infof(ctx, "starting daemon on %s (pid %d)", listen, os.Getpid())

	return d.Start(ctx)
}
