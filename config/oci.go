package config

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/utils"
)

// EnsureOCIDirs creates all required directories.
func (c *Config) EnsureOCIDirs() error {
	return utils.EnsureDirs(
		c.DBDir(),
		c.TempDir(),
		c.BlobsDir(),
		c.BootBaseDir(),
	)
}

// Derived path helpers. All OCI data lives under {RootDir}/oci/.

func (c *Config) ociDir() string         { return filepath.Join(c.RootDir, "oci") }
func (c *Config) DBDir() string          { return filepath.Join(c.ociDir(), "db") }
func (c *Config) TempDir() string        { return filepath.Join(c.ociDir(), "temp") }
func (c *Config) BlobsDir() string       { return filepath.Join(c.ociDir(), "blobs") }
func (c *Config) BootBaseDir() string    { return filepath.Join(c.ociDir(), "boot") }
func (c *Config) ImageIndexFile() string { return filepath.Join(c.DBDir(), "images.json") }
func (c *Config) ImageIndexLock() string { return filepath.Join(c.DBDir(), "images.lock") }

func (c *Config) BlobPath(layerDigestHex string) string {
	return filepath.Join(c.BlobsDir(), layerDigestHex+".erofs")
}

func (c *Config) BootDir(layerDigestHex string) string {
	return filepath.Join(c.BootBaseDir(), layerDigestHex)
}

func (c *Config) KernelPath(layerDigestHex string) string {
	return filepath.Join(c.BootDir(layerDigestHex), "vmlinuz")
}

func (c *Config) InitrdPath(layerDigestHex string) string {
	return filepath.Join(c.BootDir(layerDigestHex), "initrd.img")
}
