package cni

import (
	"fmt"
	"runtime"
	"syscall"
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

// setupTCRedirect enters the target netns and wires ifName ↔ tapName
// using TC ingress + mirred redirect (no bridge needed).
func setupTCRedirect(nsPath, ifName, tapName string) error {
	return cns.WithNetNSPath(nsPath, func(_ cns.NetNS) error {
		return tcRedirectInNS(ifName, tapName)
	})
}

// tcRedirectInNS runs inside the target netns.
//  1. Flush IP from ifName (guest owns it, not the netns).
//  2. Create tap device.
//  3. Bring both interfaces up.
//  4. Attach ingress qdisc to both.
//  5. Add U32+mirred filters for bidirectional redirect.
func tcRedirectInNS(ifName, tapName string) error {
	// 1. Find CNI veth and flush its IP addresses.
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

	// 2. Create tap device.
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

	// 3. Bring both interfaces up.
	for _, l := range []netlink.Link{link, tapLink} {
		if upErr := netlink.LinkSetUp(l); upErr != nil {
			return fmt.Errorf("set %s up: %w", l.Attrs().Name, upErr)
		}
	}

	// 4. Attach ingress qdisc to both interfaces.
	for _, l := range []netlink.Link{link, tapLink} {
		qdisc := &netlink.Ingress{
			QdiscAttrs: netlink.QdiscAttrs{
				LinkIndex: l.Attrs().Index,
				Parent:    netlink.HANDLE_INGRESS,
			},
		}
		if qdiscErr := netlink.QdiscAdd(qdisc); qdiscErr != nil {
			return fmt.Errorf("add ingress qdisc on %s: %w", l.Attrs().Name, qdiscErr)
		}
	}

	// 5. Bidirectional redirect: eth0 ingress → tap0, tap0 ingress → eth0.
	if err := addTCRedirect(link, tapLink); err != nil {
		return fmt.Errorf("redirect %s -> %s: %w", ifName, tapName, err)
	}
	if err := addTCRedirect(tapLink, link); err != nil {
		return fmt.Errorf("redirect %s -> %s: %w", tapName, ifName, err)
	}
	return nil
}

// addTCRedirect adds a U32 catch-all filter on from's ingress that redirects
// all packets to to's egress via mirred. TC_ACT_STOLEN ensures the packet is
// consumed and never reaches the netns host stack.
func addTCRedirect(from, to netlink.Link) error {
	filter := &netlink.U32{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: from.Attrs().Index,
			Parent:    netlink.HANDLE_INGRESS,
			Priority:  1,
			Protocol:  syscall.ETH_P_ALL,
		},
		Sel: &netlink.TcU32Sel{
			Flags: netlink.TC_U32_TERMINAL,
			Keys: []netlink.TcU32Key{
				{Mask: 0x0, Val: 0x0, Off: 0, OffMask: 0x0},
			},
		},
		Actions: []netlink.Action{
			&netlink.MirredAction{
				ActionAttrs:  netlink.ActionAttrs{Action: netlink.TC_ACT_STOLEN},
				MirredAction: netlink.TCA_EGRESS_REDIR,
				Ifindex:      to.Attrs().Index,
			},
		},
	}
	return netlink.FilterAdd(filter)
}
