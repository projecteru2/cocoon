package cni

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/utils"
)

const (
	netnsBasePath = "/var/run/netns"
	// netnsPrefix prevents GC from deleting netns created by other tools
	// (docker, containerd, etc.). Only netns matching this prefix are managed.
	netnsPrefix = "cocoon-"
)

// Config holds CNI network provider specific configuration, embedding the global config.
type Config struct {
	config.Config
}

// EnsureDirs creates all static directories required by the CNI network provider.
func (c *Config) EnsureDirs() error {
	return utils.EnsureDirs(
		c.dbDir(),
	)
}

func (c *Config) IndexFile() string { return filepath.Join(c.dbDir(), "networks.json") }
func (c *Config) IndexLock() string { return filepath.Join(c.dbDir(), "networks.lock") }
func (c *Config) CacheDir() string  { return filepath.Join(c.dir(), "cache") }

func (c *Config) dir() string   { return filepath.Join(c.RootDir, "cni") }
func (c *Config) dbDir() string { return filepath.Join(c.dir(), "db") }

// netnsPath returns the named netns path for a VM.
func netnsPath(vmID string) string {
	return filepath.Join(netnsBasePath, netnsPrefix+vmID)
}

// netnsName returns the named netns name (without path) for a VM.
func netnsName(vmID string) string {
	return netnsPrefix + vmID
}
