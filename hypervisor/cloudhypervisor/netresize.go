package cloudhypervisor

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/extend/netresize"
	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/network"
	"github.com/cocoonstack/cocoon/types"
)

// NetResize brings the VM's NIC count to spec.Target on a running CH VM.
func (ch *CloudHypervisor) NetResize(ctx context.Context, vmRef string, spec netresize.Spec, plumbing netresize.Plumbing) (netresize.Result, error) {
	if err := spec.Normalize(); err != nil {
		return netresize.Result{}, err
	}
	hc, vmID, rec, err := ch.runningVMClientWithRecord(ctx, vmRef)
	if err != nil {
		return netresize.Result{}, err
	}
	current := len(rec.NetworkConfigs)
	res := netresize.Result{Before: current, After: current}
	switch {
	case spec.Target == current:
		return res, nil
	case spec.Target > current:
		return ch.netResizeAdd(ctx, hc, vmID, &rec.Config, plumbing, current, spec.Target, res)
	default:
		info, infoErr := getVMInfo(ctx, hc)
		if infoErr != nil {
			return res, infoErr
		}
		return ch.netResizeRemove(ctx, hc, info, vmID, rec.NetworkConfigs, plumbing, current, spec.Target, res)
	}
}

func (ch *CloudHypervisor) netResizeAdd(ctx context.Context, hc *http.Client, vmID string, vmCfg *types.VMConfig, plumbing netresize.Plumbing, from, target int, res netresize.Result) (netresize.Result, error) {
	logger := log.WithFunc("cloudhypervisor.NetResize.add")
	for i := from; i < target; i++ {
		ncs, err := plumbing.Add(ctx, vmID, vmCfg, network.AddSpec{Index: i})
		if err != nil {
			return res, fmt.Errorf("nic %d host plumbing: %w", i, err)
		}
		if len(ncs) != 1 || ncs[0] == nil {
			return res, fmt.Errorf("nic %d: plumbing returned %d configs", i, len(ncs))
		}
		nc := ncs[0]
		chID, err := addCocoonNIC(ctx, hc, nc)
		if err != nil {
			if rmErr := plumbing.Remove(ctx, vmID, i); rmErr != nil {
				logger.Warnf(ctx, "rollback host plumbing for nic %d: %v", i, rmErr)
			}
			return res, fmt.Errorf("vm.add-net nic %d: %w", i, err)
		}
		if err := ch.appendNetworkConfig(ctx, vmID, nc); err != nil {
			// without rollback the deterministic id collides on the next resize.
			if rmErr := removeDeviceVM(ctx, hc, chID); rmErr != nil {
				logger.Warnf(ctx, "rollback vm.remove-device %s after persist failure: %v", chID, rmErr)
			}
			if rmErr := plumbing.Remove(ctx, vmID, i); rmErr != nil {
				logger.Warnf(ctx, "rollback host plumbing for nic %d: %v", i, rmErr)
			}
			return res, fmt.Errorf("persist nic %d: %w", i, err)
		}
		res.Added = append(res.Added, netresize.NIC{Index: i, TAP: nc.TAP, MAC: nc.MAC})
		res.After = i + 1
	}
	return res, nil
}

func (ch *CloudHypervisor) netResizeRemove(ctx context.Context, hc *http.Client, info *chVMInfoResponse, vmID string, ncs []*types.NetworkConfig, plumbing netresize.Plumbing, current, target int, res netresize.Result) (netresize.Result, error) {
	logger := log.WithFunc("cloudhypervisor.NetResize.remove")
	macToID := make(map[string]string, len(info.Config.Nets))
	for _, n := range info.Config.Nets {
		macToID[strings.ToLower(n.MAC)] = n.ID
	}
	for i := current - 1; i >= target; i-- {
		nc := ncs[i]
		chID := macToID[strings.ToLower(nc.MAC)]
		if chID == "" {
			return res, fmt.Errorf("nic %d MAC %s: no live device", i, nc.MAC)
		}
		if err := removeDeviceVM(ctx, hc, chID); err != nil {
			return res, fmt.Errorf("vm.remove-device nic %d (%s): %w", i, chID, err)
		}
		// CH eject is irrevocable; truncate even if plumbing leaks, else the
		// next resize re-reads the stale NIC and fails MAC lookup.
		plumbingErr := plumbing.Remove(ctx, vmID, i)
		if err := ch.truncateNetworkConfigs(ctx, vmID, i); err != nil {
			logger.Errorf(ctx, err, "persistence diverged from CH for vm %s nic %d (%s): live device removed, cocoon record retained", vmID, i, chID)
			return res, fmt.Errorf("persist remove nic %d: %w", i, err)
		}
		if plumbingErr != nil {
			msg := fmt.Sprintf("nic %d (%s) host plumbing leaked, stop+restart will reclaim: %v", i, chID, plumbingErr)
			logger.Warn(ctx, msg)
			res.Warnings = append(res.Warnings, msg)
		}
		res.Removed = append(res.Removed, netresize.NIC{Index: i, TAP: nc.TAP, MAC: nc.MAC})
		res.After = i
	}
	return res, nil
}

func (ch *CloudHypervisor) appendNetworkConfig(ctx context.Context, vmID string, nc *types.NetworkConfig) error {
	return ch.DB.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r, err := idx.GetRecord(vmID)
		if err != nil {
			return err
		}
		r.NetworkConfigs = append(r.NetworkConfigs, nc)
		return nil
	})
}

func (ch *CloudHypervisor) truncateNetworkConfigs(ctx context.Context, vmID string, length int) error {
	return ch.DB.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r, err := idx.GetRecord(vmID)
		if err != nil {
			return err
		}
		if length < len(r.NetworkConfigs) {
			r.NetworkConfigs = r.NetworkConfigs[:length]
		}
		return nil
	})
}
