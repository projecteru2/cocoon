package cni

import (
	"fmt"
	"os"
	"runtime"

	cns "github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"

	"github.com/projecteru2/cocoon/config"
)

// createNetns creates a named network namespace at /var/run/netns/{name}.
func createNetns(name string) error {
	if err := os.MkdirAll(config.NetnsPath, 0o755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", config.NetnsPath, err)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("get current netns: %w", err)
	}
	defer origNS.Close() //nolint:errcheck

	newNS, err := netns.New()
	if err != nil {
		return fmt.Errorf("create netns: %w", err)
	}
	defer newNS.Close() //nolint:errcheck

	nsPath := config.NetnsPath + "/" + name
	if createErr := os.WriteFile(nsPath, nil, 0o444); createErr != nil { //nolint:gosec
		_ = netns.Set(origNS)
		return fmt.Errorf("create mount point %s: %w", nsPath, createErr)
	}

	if setErr := netns.Set(origNS); setErr != nil {
		return fmt.Errorf("restore netns: %w", setErr)
	}

	if mountErr := mountNetns(newNS, nsPath); mountErr != nil {
		_ = os.Remove(nsPath)
		return fmt.Errorf("bind mount netns %s: %w", name, mountErr)
	}
	return nil
}

// deleteNetns removes a named network namespace.
func deleteNetns(name string) error {
	nsPath := config.NetnsPath + "/" + name
	if err := unmountNetns(nsPath); err != nil {
		return fmt.Errorf("unmount netns %s: %w", name, err)
	}
	return os.Remove(nsPath)
}

// setupBridgeTap enters the target netns via the CNI plugins/pkg/ns closure
// and sets up bridge + tap using netlink. The closure handles LockOSThread,
// setns, and restore automatically.
//
//  1. Flush IP from ifName (guest owns it, not the netns).
//  2. Create bridge, create tap.
//  3. Enslave ifName and tap to bridge.
//  4. Bring everything up.
func setupBridgeTap(nsPath, ifName, brName, tapName string) error {
	return cns.WithNetNSPath(nsPath, func(_ cns.NetNS) error {
		return bridgeTapInNS(ifName, brName, tapName)
	})
}

// bridgeTapInNS runs inside the target netns (called by setupBridgeTap).
func bridgeTapInNS(ifName, brName, tapName string) error {
	// 1. Flush addresses from the CNI interface (guest owns the IP).
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("find %s: %w", ifName, err)
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("list addrs on %s: %w", ifName, err)
	}
	for _, addr := range addrs {
		if delErr := netlink.AddrDel(link, &addr); delErr != nil {
			return fmt.Errorf("flush addr %s on %s: %w", addr.IPNet, ifName, delErr)
		}
	}

	// 2. Create bridge.
	br := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: brName}}
	if addErr := netlink.LinkAdd(br); addErr != nil {
		return fmt.Errorf("add bridge %s: %w", brName, addErr)
	}
	brLink, err := netlink.LinkByName(brName)
	if err != nil {
		return fmt.Errorf("find bridge %s: %w", brName, err)
	}

	// 3. Create tap.
	tap := &netlink.Tuntap{
		LinkAttrs: netlink.LinkAttrs{Name: tapName},
		Mode:      netlink.TUNTAP_MODE_TAP,
	}
	if addErr := netlink.LinkAdd(tap); addErr != nil {
		return fmt.Errorf("add tap %s: %w", tapName, addErr)
	}
	tapLink, err := netlink.LinkByName(tapName)
	if err != nil {
		return fmt.Errorf("find tap %s: %w", tapName, err)
	}

	// 4. Enslave to bridge.
	if err := netlink.LinkSetMaster(link, brLink); err != nil {
		return fmt.Errorf("set %s master %s: %w", ifName, brName, err)
	}
	if err := netlink.LinkSetMaster(tapLink, brLink); err != nil {
		return fmt.Errorf("set %s master %s: %w", tapName, brName, err)
	}

	// 5. Bring up.
	for _, l := range []netlink.Link{link, tapLink, brLink} {
		if err := netlink.LinkSetUp(l); err != nil {
			return fmt.Errorf("set %s up: %w", l.Attrs().Name, err)
		}
	}
	return nil
}
