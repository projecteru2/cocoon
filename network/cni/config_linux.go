package cni

import (
	"fmt"
	"runtime"
	"time"

	cns "github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// createNetns creates a named network namespace at /run/netns/{name}.
// netns.NewNamed is NOT thread-safe (no LockOSThread, no netns restore),
// so we handle that here.
func createNetns(name string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Save current netns to restore after NewNamed pollutes the thread.
	origNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("get current netns: %w", err)
	}
	defer origNS.Close() //nolint:errcheck

	ns, err := netns.NewNamed(name)
	if err != nil {
		return fmt.Errorf("create netns %s: %w", name, err)
	}
	_ = ns.Close()

	// Restore: NewNamed leaves the thread in the new netns.
	if err := netns.Set(origNS); err != nil {
		return fmt.Errorf("restore netns: %w", err)
	}
	return nil
}

// deleteNetns removes a named network namespace.
// Retries briefly because the kernel may still hold a reference to the netns
// right after the CH process is killed (fd cleanup is asynchronous).
func deleteNetns(name string) error {
	for range 10 {
		if err := netns.DeleteNamed(name); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return netns.DeleteNamed(name)
}

// setupBridgeTap enters the target netns via the CNI plugins/pkg/ns closure
// and configures bridge + tap using netlink.
func setupBridgeTap(nsPath, ifName, brName, tapName string) error {
	return cns.WithNetNSPath(nsPath, func(_ cns.NetNS) error {
		return bridgeTapInNS(ifName, brName, tapName)
	})
}

// bridgeTapInNS runs inside the target netns.
//  1. Flush IP from ifName (guest owns it, not the netns).
//  2. Create bridge + tap.
//  3. Enslave ifName and tap to bridge.
//  4. Bring everything up.
func bridgeTapInNS(ifName, brName, tapName string) error {
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

	br := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: brName}}
	if addErr := netlink.LinkAdd(br); addErr != nil {
		return fmt.Errorf("add bridge %s: %w", brName, addErr)
	}
	brLink, err := netlink.LinkByName(brName)
	if err != nil {
		return fmt.Errorf("find bridge %s: %w", brName, err)
	}

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

	if masterErr := netlink.LinkSetMaster(link, brLink); masterErr != nil {
		return fmt.Errorf("set %s master %s: %w", ifName, brName, masterErr)
	}
	if masterErr := netlink.LinkSetMaster(tapLink, brLink); masterErr != nil {
		return fmt.Errorf("set %s master %s: %w", tapName, brName, masterErr)
	}

	for _, l := range []netlink.Link{link, tapLink, brLink} {
		if upErr := netlink.LinkSetUp(l); upErr != nil {
			return fmt.Errorf("set %s up: %w", l.Attrs().Name, upErr)
		}
	}
	return nil
}
