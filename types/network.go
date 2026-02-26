package types

// NetworkConfig describes a single NIC attached to a VM.
type NetworkConfig struct {
	Tap       string `json:"tap"`
	Mac       string `json:"mac"`
	Queue     int64  `json:"queue"`
	QueueSize int64  `json:"queue_size"`

	// Guest-side IP configuration returned by the network plugin.
	// nil means DHCP / no static config.
	Network *Network `json:"network,omitempty"`
}

// Network holds guest-visible IP configuration for a NIC.
// All addresses are stored as human-readable strings for JSON clarity.
type Network struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	IP       string `json:"ip"`                // dotted decimal, e.g. "10.0.0.2"
	Gateway  string `json:"gateway,omitempty"` // dotted decimal, e.g. "10.0.0.1"
	Prefix   int    `json:"prefix"`            // CIDR prefix length, e.g. 24
	Hostname string `json:"hostname,omitempty"`
}
