package types

import (
	"fmt"
	"regexp"
	"time"
)

const (
	VMStateCreating VMState = "creating" // DB placeholder written, dirs/disks being prepared
	VMStateCreated  VMState = "created"  // registered, CH process not yet started
	VMStateRunning  VMState = "running"  // CH process alive, guest is up
	VMStateStopped  VMState = "stopped"  // CH process has exited cleanly
	VMStateError    VMState = "error"    // start or stop failed
)

var (
	validName     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)
	validUsername = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	// shellUnsafe matches characters that could cause shell injection in
	// cloud-init runcmd (backticks, $, semicolons, pipes, etc.).
	shellUnsafe = regexp.MustCompile("[`$;|&(){}\\\\<>!]")
)

// VMState represents the lifecycle state of a VM.
type VMState string

// VMConfig describes the resources requested for a new VM.
type VMConfig struct {
	Config
	Name string `json:"name"`

	OnDemand  bool           `json:"-"` // use UFFD on-demand memory restore (CH only); transient, not persisted
	User      string         `json:"-"`
	Password  string         `json:"-"`
	DataDisks []DataDiskSpec `json:"-"` // populated from --data-disk; consumed by Create
}

// NetSetup is the network handoff from initNetwork to the hypervisor.
type NetSetup struct {
	Backend   string
	NetnsPath string
	BridgeDev string
	NICs      []*NetworkConfig
}

// VM is the runtime record for a VM, persisted by the hypervisor backend.
type VM struct {
	ID         string   `json:"id"`
	Hypervisor string   `json:"hypervisor,omitempty"`
	State      VMState  `json:"state"`
	Config     VMConfig `json:"config"`

	// Runtime — populated only while State == VMStateRunning.
	PID         int    `json:"pid"`
	SocketPath  string `json:"socket_path,omitempty"`  // CH API Unix socket
	VsockSocket string `json:"vsock_socket,omitempty"` // hybrid vsock UDS for cocoon-agent

	// Attached resources — promoted into VMRecord via embedding.
	NetworkConfigs []*NetworkConfig `json:"network_configs,omitempty"`
	StorageConfigs []*StorageConfig `json:"storage_configs,omitempty"`

	// VM-level so 0-NIC VMs still carry backend + netns.
	NetBackend   string `json:"net_backend,omitempty"`
	NetnsPath    string `json:"netns_path,omitempty"`
	NetBridgeDev string `json:"net_bridge_dev,omitempty"`

	// FirstBooted is true after the VM has been started at least once.
	// Used to skip cidata attachment on subsequent starts (cloudimg only).
	FirstBooted bool `json:"first_booted"`

	// SnapshotIDs tracks snapshots created from this VM.
	// Populated at runtime by toVM() from VMRecord.SnapshotIDs.
	SnapshotIDs map[string]struct{} `json:"snapshot_ids,omitempty"`

	// Timestamps.
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
}

// Validate checks that VMConfig fields are within acceptable ranges.
func (cfg *VMConfig) Validate() error {
	if cfg.Name == "" {
		return fmt.Errorf("vm name cannot be empty")
	}
	if !validName.MatchString(cfg.Name) {
		return fmt.Errorf("vm name %q is invalid: must match %s (max 63 chars)", cfg.Name, validName.String())
	}
	if cfg.CPU <= 0 {
		return fmt.Errorf("--cpu must be at least 1, got %d", cfg.CPU)
	}
	if cfg.Memory < 512<<20 {
		return fmt.Errorf("--memory must be at least 512M, got %d", cfg.Memory)
	}
	if cfg.Storage < 10<<30 {
		return fmt.Errorf("--storage must be at least 10G, got %d", cfg.Storage)
	}
	if cfg.QueueSize < 0 {
		return fmt.Errorf("--queue-size must be non-negative, got %d", cfg.QueueSize)
	}
	if cfg.DiskQueueSize < 0 {
		return fmt.Errorf("--disk-queue-size must be non-negative, got %d", cfg.DiskQueueSize)
	}
	if cfg.User != "" && !validUsername.MatchString(cfg.User) {
		return fmt.Errorf("--user %q is invalid: must be a lowercase Linux username (letters, digits, underscores, hyphens)", cfg.User)
	}
	if cfg.Password != "" && shellUnsafe.MatchString(cfg.Password) {
		return fmt.Errorf("--password contains unsafe shell characters (backtick, $, ;, |, &, etc.)")
	}
	return nil
}

// ResolvedNetnsPath returns NetnsPath, with NIC[0] fallback.
func (v *VM) ResolvedNetnsPath() string {
	if v == nil {
		return ""
	}
	if v.NetnsPath != "" {
		return v.NetnsPath
	}
	if len(v.NetworkConfigs) > 0 {
		return v.NetworkConfigs[0].NetnsPath
	}
	return ""
}

// ResolvedNetBackend returns NetBackend, with NIC[0] fallback.
func (v *VM) ResolvedNetBackend() string {
	if v == nil {
		return ""
	}
	if v.NetBackend != "" {
		return v.NetBackend
	}
	if len(v.NetworkConfigs) > 0 {
		if b := v.NetworkConfigs[0].Backend; b != "" {
			return b
		}
		return BackendCNI
	}
	return ""
}

// ResolvedNetBridgeDev returns NetBridgeDev, with NIC[0] fallback.
func (v *VM) ResolvedNetBridgeDev() string {
	if v == nil {
		return ""
	}
	if v.NetBridgeDev != "" {
		return v.NetBridgeDev
	}
	if len(v.NetworkConfigs) > 0 {
		return v.NetworkConfigs[0].BridgeDev
	}
	return ""
}

// ApplyNetSetup copies network fields from net into v.
func (v *VM) ApplyNetSetup(net NetSetup) {
	if v == nil {
		return
	}
	v.NetworkConfigs = net.NICs
	v.NetBackend = net.Backend
	v.NetnsPath = net.NetnsPath
	v.NetBridgeDev = net.BridgeDev
}
