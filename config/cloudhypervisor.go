package config

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/utils"
)

// EnsureCHDirs creates all static directories required by the Cloud Hypervisor backend.
// Per-VM runtime and log directories are created on demand via EnsureCHVMDirs.
func (c *Config) EnsureCHDirs() error {
	return utils.EnsureDirs(
		c.chDBDir(),
		c.chLogDir(),
	)
}

// EnsureCHVMDirs creates per-VM runtime and log directories.
// Called when a VM is created or started.
func (c *Config) EnsureCHVMDirs(vmID string) error {
	return utils.EnsureDirs(
		c.CHVMRunDir(vmID),
		c.CHVMLogDir(vmID),
	)
}

// Derived path helpers. All CH persistent data lives under {RootDir}/cloudhypervisor/.

func (c *Config) chDir() string   { return filepath.Join(c.RootDir, "cloudhypervisor") }
func (c *Config) chDBDir() string { return filepath.Join(c.chDir(), "db") }

// CHIndexFile and CHIndexLock are the VM index store paths.
func (c *Config) CHIndexFile() string { return filepath.Join(c.chDBDir(), "vms.json") }
func (c *Config) CHIndexLock() string { return filepath.Join(c.chDBDir(), "vms.lock") }

// Runtime paths (per VM, under RunDir — ephemeral).

func (c *Config) CHVMRunDir(vmID string) string  { return filepath.Join(c.RunDir, "cloudhypervisor", vmID) }
func (c *Config) CHVMSocketPath(vmID string) string { return filepath.Join(c.CHVMRunDir(vmID), "api.sock") }
func (c *Config) CHVMPIDFile(vmID string) string    { return filepath.Join(c.CHVMRunDir(vmID), "ch.pid") }

// Log paths (per VM, under {LogDir}/cloudhypervisor/).
// Namespaced under "cloudhypervisor" so future backends (e.g. qemu) scan
// their own subdirectory and never conflict during GC.

func (c *Config) chLogDir() string                   { return filepath.Join(c.LogDir, "cloudhypervisor") }
func (c *Config) CHVMLogDir(vmID string) string      { return filepath.Join(c.chLogDir(), vmID) }
func (c *Config) CHVMSerialLog(vmID string) string   { return filepath.Join(c.CHVMLogDir(vmID), "serial.log") }
func (c *Config) CHVMProcessLog(vmID string) string  { return filepath.Join(c.CHVMLogDir(vmID), "ch.log") }

// FirmwarePath returns the path to the UEFI firmware blob (CLOUDHV.fd).
func (c *Config) FirmwarePath() string {
	return filepath.Join(c.RootDir, "firmware", "CLOUDHV.fd")
}
