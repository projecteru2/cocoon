package firecracker

import (
	"path/filepath"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/hypervisor"
)

// Config holds Firecracker specific configuration.
type Config struct {
	hypervisor.BaseConfig
}

// NewConfig creates a Config from a global config.
func NewConfig(conf *config.Config) *Config {
	return &Config{BaseConfig: hypervisor.NewBaseConfig(conf, "firecracker")}
}

// BinaryName returns the base name of the Firecracker binary.
func (c *Config) BinaryName() string { return filepath.Base(c.FCBinary) }

// PIDFileName returns the PID file name for the Firecracker backend.
func (c *Config) PIDFileName() string { return pidFileName }

// COWRawPath returns the path for the OCI COW raw disk.
func (c *Config) COWRawPath(vmID string) string {
	return filepath.Join(c.VMRunDir(vmID), cowFileName)
}
