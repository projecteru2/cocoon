package cni

import (
	"context"
	"fmt"
	"net"
	"os/exec"

	"github.com/containernetworking/cni/libcni"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/network"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// Config creates the network namespace, runs CNI ADD for each NIC, sets up
// bridge + tap inside the netns, and returns NetworkConfigs ready for CH --net.
//
// Flow per NIC (from issue #1):
//  1. ip netns add cocoon-{vmID}
//  2. CNI ADD (containerID=vmID, netns=cocoon-{vmID}, ifName=eth{i})
//  3. Inside netns: flush eth{i} IP, create br{i}+tap{i}, bridge them
//  4. Return NetworkConfig{Tap: "tap{i}", Mac: generated, Network: CNI result}
func (c *CNI) Config(ctx context.Context, vmID string, numNICs int, vmCfg *types.VMConfig) (configs []*types.NetworkConfig, retErr error) {
	if c.networkConfList == nil || c.cniConf == nil {
		return nil, fmt.Errorf("%w: no conflist found in %s", network.ErrNotConfigured, c.conf.CNIConfDir)
	}
	logger := log.WithFunc("cni.Config")

	nsName := c.conf.CNINetnsName(vmID)
	nsPath := c.conf.CNINetnsPath(vmID)

	// Step 1: create named network namespace.
	if out, err := exec.CommandContext(ctx, "ip", "netns", "add", nsName).CombinedOutput(); err != nil { //nolint:gosec
		return nil, fmt.Errorf("ip netns add %s: %s: %w", nsName, out, err)
	}
	// If anything fails after netns creation, tear it down.
	defer func() {
		if retErr != nil {
			_ = exec.CommandContext(ctx, "ip", "netns", "del", nsName).Run() //nolint:gosec
		}
	}()

	for i := range numNICs {
		ifName := fmt.Sprintf("eth%d", i)
		tapName := fmt.Sprintf("tap%d", i)
		brName := fmt.Sprintf("br%d", i)

		// Step 2: CNI ADD — creates veth pair, assigns IP via IPAM.
		rt := &libcni.RuntimeConf{
			ContainerID: vmID,
			NetNS:       nsPath,
			IfName:      ifName,
		}
		cniResult, err := c.cniConf.AddNetworkList(ctx, c.networkConfList, rt)
		if err != nil {
			return nil, fmt.Errorf("CNI ADD %s/%s: %w", vmID, ifName, err)
		}

		// Parse the CNI result to extract IP/Gateway/Mask.
		netInfo, err := extractNetworkInfo(cniResult, vmID, i)
		if err != nil {
			return nil, fmt.Errorf("parse CNI result: %w", err)
		}

		// Step 3: inside netns — flush IP, create bridge + tap.
		if setupErr := setupBridgeTap(ctx, nsName, ifName, brName, tapName); setupErr != nil {
			return nil, fmt.Errorf("setup bridge/tap %s: %w", vmID, setupErr)
		}

		// Generate MAC for CH --net.
		mac, err := utils.GenerateMAC()
		if err != nil {
			return nil, err
		}

		configs = append(configs, &types.NetworkConfig{
			Tap:       tapName,
			Mac:       mac.String(),
			Queue:     int64(vmCfg.CPU),
			QueueSize: 256, //nolint:mnd
			Network:   netInfo,
		})

		logger.Infof(ctx, "NIC %d: %s ip=%s gw=%s tap=%s mac=%s",
			i, ifName, netInfo.IP, netInfo.Gateway, tapName, mac)
	}

	// Step 4: persist network records to DB.
	if err := c.store.Update(ctx, func(idx *networkIndex) error {
		for i, cfg := range configs {
			netID, genErr := utils.GenerateID()
			if genErr != nil {
				return genErr
			}
			cfg.Network.ID = netID
			cfg.Network.Type = c.networkConfList.Name
			idx.Networks[netID] = &networkRecord{
				Network: *cfg.Network,
				VMID:    vmID,
				IfName:  fmt.Sprintf("eth%d", i),
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("persist network records: %w", err)
	}

	return configs, nil
}

// setupBridgeTap runs commands inside the netns to:
//  1. Flush the IP from eth{i} (guest owns it, not the netns)
//  2. Create a bridge and tap device
//  3. Enslave eth{i} and tap to the bridge
//  4. Bring everything up
func setupBridgeTap(ctx context.Context, nsName, ifName, brName, tapName string) error {
	nsexec := func(args ...string) error {
		cmd := append([]string{"netns", "exec", nsName}, args...)
		out, err := exec.CommandContext(ctx, "ip", cmd...).CombinedOutput() //nolint:gosec
		if err != nil {
			return fmt.Errorf("ip %v: %s: %w", args, out, err)
		}
		return nil
	}

	steps := [][]string{
		{"addr", "flush", "dev", ifName},
		{"link", "add", brName, "type", "bridge"},
		{"link", "set", ifName, "master", brName},
		{"tuntap", "add", tapName, "mode", "tap"},
		{"link", "set", tapName, "master", brName},
		{"link", "set", ifName, "up"},
		{"link", "set", tapName, "up"},
		{"link", "set", brName, "up"},
	}
	for _, args := range steps {
		if err := nsexec(args...); err != nil {
			return err
		}
	}
	return nil
}

// extractNetworkInfo parses the CNI ADD result into types.Network.
func extractNetworkInfo(result cnitypes.Result, vmID string, nicIdx int) (*types.Network, error) {
	newResult, err := current.NewResultFromResult(result)
	if err != nil {
		return nil, fmt.Errorf("convert CNI result: %w", err)
	}
	if len(newResult.IPs) == 0 {
		return nil, fmt.Errorf("CNI returned no IPs for %s NIC %d", vmID, nicIdx)
	}

	ip := newResult.IPs[0]
	ones, bits := ip.Address.Mask.Size()

	info := &types.Network{
		IP:      ip.Address.IP,
		Netmask: net.CIDRMask(ones, bits),
	}
	if ip.Gateway != nil {
		info.Gateway = ip.Gateway
	}
	return info, nil
}
