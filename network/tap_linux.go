//go:build linux

package network

import (
	"github.com/vishvananda/netlink"
)

const (
	// tapTxQueueLen absorbs traffic bursts (especially UDP) without
	// dropping; the kernel default of 1000 is too small for VM workloads.
	tapTxQueueLen = 10000

	// groMaxSize matches the maximum virtio-net segment size, allowing
	// the kernel to aggregate inbound packets before CH reads them.
	groMaxSize = 65536
)

// TuneTAP applies best-effort performance tuning to a TAP device.
func TuneTAP(link netlink.Link) error {
	if err := netlink.LinkSetTxQLen(link, tapTxQueueLen); err != nil {
		return err
	}
	return netlink.LinkSetGROMaxSize(link, groMaxSize)
}
