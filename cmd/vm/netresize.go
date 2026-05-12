package vm

import (
	"fmt"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	cmdcore "github.com/cocoonstack/cocoon/cmd/core"
	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/extend/netresize"
	"github.com/cocoonstack/cocoon/network"
	"github.com/cocoonstack/cocoon/types"
)

func (h Handler) NetResize(cmd *cobra.Command, args []string) error {
	ctx, conf, hyper, resizer, err := resolveAttacher[netresize.Resizer](h, cmd, args, "vm net", netresize.ErrUnsupportedBackend)
	if err != nil {
		return err
	}
	vm, err := hyper.Inspect(ctx, args[0])
	if err != nil {
		return fmt.Errorf("vm net: %w", err)
	}
	plumbing, err := plumbingForVM(conf, vm.NetworkConfigs)
	if err != nil {
		return fmt.Errorf("vm net: %w", err)
	}
	target, _ := cmd.Flags().GetInt("nics")
	keepHost, _ := cmd.Flags().GetBool("keep-host-on-remove")
	res, err := resizer.NetResize(ctx, args[0], netresize.Spec{Target: target, KeepHostOnRemove: keepHost}, plumbing)
	if err != nil {
		return classifyAttachErr(err)
	}
	if done, jsonErr := cmdcore.MaybeOutputJSON(cmd, res); done {
		return jsonErr
	}
	log.WithFunc("cmd.vm.net").Infof(ctx, "resized %s: before=%d after=%d added=%d removed=%d",
		args[0], res.Before, res.After, len(res.Added), len(res.Removed))
	return nil
}

// plumbingForVM picks the network provider matching the VM's existing NICs.
// Requires at least one NIC: VMConfig has no bridge hint, so a zero-NIC VM
// can't be resized up without losing the original provider choice.
func plumbingForVM(conf *config.Config, configs []*types.NetworkConfig) (network.Network, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("vm has zero NICs; resize up is not supported (start the VM with at least one NIC)")
	}
	if configs[0].Backend == types.BackendBridge {
		if configs[0].BridgeDev == "" {
			return nil, fmt.Errorf("bridge backend without bridge device")
		}
		return cmdcore.InitBridgeNetwork(conf, configs[0].BridgeDev)
	}
	return cmdcore.InitNetwork(conf)
}
