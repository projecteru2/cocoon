package types

import "net"

type NetworkConfig struct {
	Tap       string `json:"tap"`
	Mac       string `json:"mac"`
	Queue     int64  `json:"queue"`
	QueueSize int64  `json:"queue_size"`

	// Guest-side IP configuration returned by the network plugin.
	// nil means DHCP / no static config.
	// offload_tso=on
	// offload_ufo=on
	// offload_csum=on
	Network *Network `json:"network,omitempty"`
}

type Network struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	IP       net.IP     `json:"ip"`
	Gateway  net.IP     `json:"gateway"`
	Netmask  net.IPMask `json:"netmask"`
	Hostname string     `json:"hostname"`
}
