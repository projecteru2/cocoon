package cloudhypervisor

import (
	"path/filepath"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/hypervisor"
)

// Config holds Cloud Hypervisor specific configuration.
type Config struct {
	hypervisor.BaseConfig
}

// NewConfig creates a Config from a global config.
func NewConfig(conf *config.Config) *Config {
	return &Config{BaseConfig: hypervisor.NewBaseConfig(conf, "cloudhypervisor")}
}

// BinaryName returns the base name of the Cloud Hypervisor binary.
func (c *Config) BinaryName() string { return filepath.Base(c.CHBinary) }

// PIDFileName returns the PID file name for the Cloud Hypervisor backend.
func (c *Config) PIDFileName() string { return "ch.pid" }

// COWRawPath returns the path for the OCI COW raw disk.
func (c *Config) COWRawPath(vmID string) string {
	return filepath.Join(c.VMRunDir(vmID), "cow.raw")
}

// OverlayPath returns the path for the cloudimg qcow2 overlay.
func (c *Config) OverlayPath(vmID string) string {
	return filepath.Join(c.VMRunDir(vmID), "overlay.qcow2")
}

// CidataPath returns the path for the cloud-init NoCloud cidata disk.
func (c *Config) CidataPath(vmID string) string {
	return filepath.Join(c.VMRunDir(vmID), cidataFile)
}
