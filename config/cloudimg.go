package config

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/utils"
)

// EnsureCloudimgDirs creates all required directories for the cloudimg backend.
func (c *Config) EnsureCloudimgDirs() error {
	return utils.EnsureDirs(
		c.CloudimgDBDir(),
		c.CloudimgTempDir(),
		c.CloudimgBlobsDir(),
	)
}

// Derived path helpers. All cloudimg data lives under {RootDir}/cloudimg/.

func (c *Config) cloudimgDir() string       { return filepath.Join(c.RootDir, "cloudimg") }
func (c *Config) CloudimgDBDir() string     { return filepath.Join(c.cloudimgDir(), "db") }
func (c *Config) CloudimgTempDir() string   { return filepath.Join(c.cloudimgDir(), "temp") }
func (c *Config) CloudimgBlobsDir() string  { return filepath.Join(c.cloudimgDir(), "blobs") }
func (c *Config) CloudimgIndexFile() string { return filepath.Join(c.CloudimgDBDir(), "images.json") }
func (c *Config) CloudimgIndexLock() string { return filepath.Join(c.CloudimgDBDir(), "images.lock") }

func (c *Config) CloudimgBlobPath(hex string) string {
	return filepath.Join(c.CloudimgBlobsDir(), hex+".qcow2")
}
