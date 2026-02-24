package types

import "time"

// VMState represents the lifecycle state of a VM from the hypervisor's perspective.
type VMState string

const (
	VMStateCreated VMState = "created" // registered, CH process not yet started
	VMStateRunning VMState = "running" // CH process alive, guest is up
	VMStateStopped VMState = "stopped" // CH process has exited cleanly
	VMStateError   VMState = "error"   // start or stop failed
)

// VMConfig describes the resources requested for a new VM.
type VMConfig struct {
	Name    string `json:"name"`
	CPU     int    `json:"cpu"`
	Memory  int64  `json:"memory"`  // bytes
	Storage int64  `json:"storage"` // COW disk size, bytes

	Image string `json:"image"`
}

// VMInfo is the runtime record for a VM, persisted by the hypervisor backend.
type VMInfo struct {
	ID     string   `json:"id"`
	State  VMState  `json:"state"`
	Config VMConfig `json:"config"`

	// Runtime — populated only while State == VMStateRunning.
	PID        int    `json:"pid,omitempty"`
	SocketPath string `json:"socket_path,omitempty"` // CH API Unix socket

	// Timestamps.
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
}
