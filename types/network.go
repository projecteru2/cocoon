package types

// NetworkConfig describes a single NIC attached to a VM.
type NetworkConfig struct {
	TAP       string `json:"tap"`
	MAC       string `json:"mac"`
	NumQueues int    `json:"num_queues"` // Virtio queue count (= CPU * 2 for multi-queue).
	QueueSize int    `json:"queue_size"`

	// Backend is the provider type ("cni" or "bridge"); empty means "cni" for
	// backward compat with pre-bridge VM records.
	Backend string `json:"backend,omitempty"`

	// BridgeDev is the Linux bridge device name; set only when Backend=="bridge".
	BridgeDev string `json:"bridge_dev,omitempty"`

	// NetnsPath is the netns where the TAP lives; empty for backends without netns (e.g. macOS vmnet).
	NetnsPath string `json:"netns_path,omitempty"`

	// Network is the guest-visible IP config; nil means DHCP.
	Network *Network `json:"network,omitempty"`
}

// Network holds guest-visible IP configuration for a NIC.
// All addresses are stored as human-readable strings for JSON clarity.
// All fields are omitempty — DHCP NICs have no static IP configuration.
type Network struct {
	IP      string `json:"ip,omitempty"`      // dotted decimal, e.g. "10.0.0.2"
	Gateway string `json:"gateway,omitempty"` // dotted decimal, e.g. "10.0.0.1"
	Prefix  int    `json:"prefix,omitempty"`  // CIDR prefix length, e.g. 24
}
