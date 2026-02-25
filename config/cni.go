package config

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/utils"
)

const (
	NetnsPath = "/var/run/netns"
	// NetnsPrefix prevents GC from deleting netns created by other tools
	// (docker, containerd, etc.). Only netns matching this prefix are managed.
	NetnsPrefix = "cocoon-"
)

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

// CNICacheDir returns the directory for CNI result cache.
func (c *Config) CNICacheDir() string { return filepath.Join(c.cniDir(), "cache") }

// CNINetnsPath returns the named netns path for a VM.
// Uses NetnsPrefix to namespace cocoon netns away from other tools.
func (c *Config) CNINetnsPath(vmID string) string {
	return filepath.Join(NetnsPath, NetnsPrefix+vmID)
}

// CNINetnsName returns the named netns name (without path) for a VM.
func (c *Config) CNINetnsName(vmID string) string {
	return NetnsPrefix + vmID
}
