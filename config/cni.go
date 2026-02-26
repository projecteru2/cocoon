package config

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/utils"
)

const netnsPath = "/var/run/netns"

// EnsureCNIDirs creates all static directories required by the CNI network provider.
func (c *Config) EnsureCNIDirs() error {
	return utils.EnsureDirs(
		c.cniDBDir(),
	)
}

func (c *Config) cniDir() string   { return filepath.Join(c.RootDir, "cni") }
func (c *Config) cniDBDir() string { return filepath.Join(c.cniDir(), "db") }

// CNIIndexFile and CNIIndexLock are the network index store paths.
func (c *Config) CNIIndexFile() string { return filepath.Join(c.cniDBDir(), "networks.json") }
func (c *Config) CNIIndexLock() string { return filepath.Join(c.cniDBDir(), "networks.lock") }

// CNIConfDir returns the directory for CNI plugin configuration files.
// Default: /etc/cni/net.d (standard CNI convention).
func (c *Config) CNIConfDir() string { return "/etc/cni/net.d" }

// CNIBinDir returns the directory for CNI plugin binaries.
// Default: /opt/cni/bin (standard CNI convention).
func (c *Config) CNIBinDir() string { return "/opt/cni/bin" }

// CNICacheDir returns the directory for CNI result cache.
func (c *Config) CNICacheDir() string { return filepath.Join(c.cniDir(), "cache") }

// CNINetnsPath returns the named netns path for a VM.
func (c *Config) CNINetnsPath(vmID string) string {
	return filepath.Join(netnsPath, vmID)
}
