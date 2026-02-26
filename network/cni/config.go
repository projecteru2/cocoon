package cni

import (
	"context"
	"fmt"

	"github.com/containernetworking/cni/libcni"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/network"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

const defaultQueueSize = 256

// Config creates the network namespace, runs CNI ADD for each NIC, sets up
// TC redirect (eth↔tap) inside the netns, and returns NetworkConfigs ready for CH --net.
//
// Flow per NIC:
//  1. Create named netns cocoon-{vmID}
//  2. CNI ADD (containerID=vmID, netns path, ifName=eth{i})
//  3. Inside netns: flush eth{i} IP, create tap{i}, wire via TC ingress mirred
//  4. Return NetworkConfig{Tap: "tap{i}", Mac: generated, Network: CNI result}
func (c *CNI) Config(ctx context.Context, vmID string, numNICs int, vmCfg *types.VMConfig) (configs []*types.NetworkConfig, retErr error) {
	if c.networkConfList == nil || c.cniConf == nil {
		return nil, fmt.Errorf("%w: no conflist found in %s", network.ErrNotConfigured, c.conf.CNIConfDir)
	}
	logger := log.WithFunc("cni.Config")

	nsName := c.conf.CNINetnsName(vmID)
	nsPath := c.conf.CNINetnsPath(vmID)

	// Step 1: create named network namespace (platform-specific).
	if err := createNetns(nsName); err != nil {
		return nil, fmt.Errorf("create netns %s: %w", nsName, err)
	}

	// Track successfully added CNI interfaces for rollback.
	var addedIFs []string
	defer func() {
		if retErr == nil {
			return
		}
		// Rollback: CNI DEL for each successfully added NIC to release IPAM.
		for _, ifn := range addedIFs {
			rt := &libcni.RuntimeConf{
				ContainerID: vmID,
				NetNS:       nsPath,
				IfName:      ifn,
			}
			if delErr := c.cniConf.DelNetworkList(ctx, c.networkConfList, rt); delErr != nil {
				logger.Warnf(ctx, "rollback CNI DEL %s/%s: %v", vmID, ifn, delErr)
			}
		}
		_ = deleteNetns(nsName)
	}()

	for i := range numNICs {
		ifName := fmt.Sprintf("eth%d", i)
		tapName := fmt.Sprintf("tap%d", i)

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
		addedIFs = append(addedIFs, ifName)

		netInfo, err := extractNetworkInfo(cniResult, vmID, i)
		if err != nil {
			return nil, fmt.Errorf("parse CNI result: %w", err)
		}

		// Step 3: inside netns — flush IP, create tap, wire via TC redirect (platform-specific).
		// Returns eth0's MAC so the guest virtio-net uses the same address,
		// required for anti-spoofing CNI plugins (Cilium, Calico eBPF, VPC ENI).
		mac, setupErr := setupTCRedirect(nsPath, ifName, tapName)
		if setupErr != nil {
			return nil, fmt.Errorf("setup tc-redirect %s: %w", vmID, setupErr)
		}

		configs = append(configs, &types.NetworkConfig{
			Tap:       tapName,
			Mac:       mac,
			Queue:     int64(vmCfg.CPU),
			QueueSize: defaultQueueSize,
			Network:   netInfo,
		})

		logger.Infof(ctx, "NIC %d: %s ip=%s gw=%s tap=%s mac=%s",
			i, ifName, netInfo.IP, netInfo.Gateway, tapName, mac)
	}

	// Step 4: persist network records to DB.
	return configs, c.store.Update(ctx, func(idx *networkIndex) error {
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
	})
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
	ones, _ := ip.Address.Mask.Size()

	info := &types.Network{
		IP:     ip.Address.IP.String(),
		Prefix: ones,
	}
	if ip.Gateway != nil {
		info.Gateway = ip.Gateway.String()
	}
	return info, nil
}
