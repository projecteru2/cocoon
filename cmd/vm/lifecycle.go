package vm

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/moby/term"
	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	cmdcore "github.com/cocoonstack/cocoon/cmd/core"
	"github.com/cocoonstack/cocoon/console"
	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/network"
	"github.com/cocoonstack/cocoon/types"
)

func (h Handler) Start(cmd *cobra.Command, args []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}

	if netProvider, netErr := cmdcore.InitNetwork(conf); netErr == nil {
		h.recoverNetwork(ctx, hyper, netProvider, args)
	}

	return batchVMCmd(ctx, "start", "started", hyper.Start, args)
}

func (h Handler) Stop(cmd *cobra.Command, args []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}

	force, _ := cmd.Flags().GetBool("force")
	timeout, _ := cmd.Flags().GetInt("timeout")

	if force {
		conf.StopTimeoutSeconds = -1
	} else if timeout > 0 {
		conf.StopTimeoutSeconds = timeout
	}

	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}
	return batchVMCmd(ctx, "stop", "stopped", hyper.Stop, args)
}

func (h Handler) Inspect(cmd *cobra.Command, args []string) error {
	ctx, hyper, err := h.initHyper(cmd)
	if err != nil {
		return err
	}

	info, err := hyper.Inspect(ctx, args[0])
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	info.State = types.VMState(cmdcore.ReconcileState(info))
	return cmdcore.OutputJSON(info)
}

func (h Handler) Console(cmd *cobra.Command, args []string) error {
	ctx, hyper, err := h.initHyper(cmd)
	if err != nil {
		return err
	}
	ref := args[0]

	conn, err := hyper.Console(ctx, ref)
	if err != nil {
		return fmt.Errorf("console: %w", err)
	}
	defer conn.Close() //nolint:errcheck

	escapeStr, _ := cmd.Flags().GetString("escape-char")
	escapeChar, err := console.ParseEscapeChar(escapeStr)
	if err != nil {
		return err
	}

	inFd := os.Stdin.Fd()
	if !term.IsTerminal(inFd) {
		return fmt.Errorf("stdin is not a terminal")
	}

	oldState, err := term.SetRawTerminal(inFd)
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer func() {
		_ = term.RestoreTerminal(inFd, oldState)
		fmt.Fprintf(os.Stderr, "\r\nDisconnected from %s.\r\n", ref)
	}()

	escapeDisplay := console.FormatEscapeChar(escapeChar)
	fmt.Fprintf(os.Stderr, "Connected to %s (escape sequence: %s.)\r\n", ref, escapeDisplay)

	rw, ok := conn.(io.ReadWriter)
	if !ok {
		return fmt.Errorf("console connection does not support writing")
	}

	if f, ok := conn.(*os.File); ok {
		cleanup := console.HandleResize(inFd, f.Fd())
		defer cleanup()
	}

	escapeKeys := []byte{escapeChar, '.'}
	if err := console.Relay(rw, escapeKeys); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	return nil
}

func (h Handler) RM(cmd *cobra.Command, args []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}
	logger := log.WithFunc("cmd.rm")

	force, _ := cmd.Flags().GetBool("force")

	deleted, deleteErr := hyper.Delete(ctx, args, force)
	for _, id := range deleted {
		logger.Infof(ctx, "deleted VM: %s", id)
	}

	if len(deleted) > 0 {
		if netProvider, initErr := cmdcore.InitNetwork(conf); initErr == nil {
			if _, delErr := netProvider.Delete(ctx, deleted); delErr != nil {
				return fmt.Errorf("vm(s) deleted but network cleanup failed: %w", delErr)
			}
		}
	}

	if deleteErr != nil {
		return fmt.Errorf("rm: %w", deleteErr)
	}
	if len(deleted) == 0 {
		logger.Info(ctx, "no VMs deleted")
	}
	return nil
}

func (h Handler) recoverNetwork(ctx context.Context, hyper hypervisor.Hypervisor, net network.Network, refs []string) {
	logger := log.WithFunc("cmd.recoverNetwork")
	for _, ref := range refs {
		vm, err := hyper.Inspect(ctx, ref)
		if err != nil || vm == nil || len(vm.NetworkConfigs) == 0 {
			continue
		}
		if net.Verify(ctx, vm.ID) == nil {
			continue
		}
		logger.Warnf(ctx, "netns missing for VM %s, recovering network", vm.ID)
		if _, recoverErr := net.Config(ctx, vm.ID, len(vm.NetworkConfigs), &vm.Config, vm.NetworkConfigs...); recoverErr != nil {
			logger.Warnf(ctx, "recover network for VM %s: %v (start will fail)", vm.ID, recoverErr)
		}
	}
}

func (h Handler) initHyper(cmd *cobra.Command) (context.Context, hypervisor.Hypervisor, error) {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return nil, nil, err
	}
	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return nil, nil, err
	}
	return ctx, hyper, nil
}
