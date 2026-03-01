package cloudhypervisor

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/utils"
)

// Config holds Cloud Hypervisor specific configuration, embedding the global config.
type Config struct {
	config.Config
}

// EnsureDirs creates all static directories required by the Cloud Hypervisor backend.
func (c *Config) EnsureDirs() error {
	return utils.EnsureDirs(
		c.dbDir(),
		c.RunDir(),
		c.LogDir(),
	)
}

// RunDir returns the top-level CH runtime directory.
func (c *Config) RunDir() string { return filepath.Join(c.Config.RunDir, "cloudhypervisor") }

// LogDir returns the top-level CH log directory.
func (c *Config) LogDir() string { return filepath.Join(c.Config.LogDir, "cloudhypervisor") }

// IndexFile returns the VM index store path.
func (c *Config) IndexFile() string { return filepath.Join(c.dbDir(), "vms.json") }

// IndexLock returns the VM index lock path.
func (c *Config) IndexLock() string { return filepath.Join(c.dbDir(), "vms.lock") }

// VMRunDir returns the per-VM runtime directory.
func (c *Config) VMRunDir(vmID string) string { return filepath.Join(c.RunDir(), vmID) }

// VMLogDir returns the per-VM log directory.
func (c *Config) VMLogDir(vmID string) string { return filepath.Join(c.LogDir(), vmID) }

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
	return filepath.Join(c.VMRunDir(vmID), "cidata.img")
}

func (c *Config) dir() string   { return filepath.Join(c.RootDir, "cloudhypervisor") }
func (c *Config) dbDir() string { return filepath.Join(c.dir(), "db") }
