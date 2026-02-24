package config

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/utils"
)

// EnsureOCIDirs creates all required directories for the OCI backend.
func (c *Config) EnsureOCIDirs() error {
	return utils.EnsureDirs(
		c.OCIDBDir(),
		c.OCITempDir(),
		c.OCIBlobsDir(),
		c.OCIBootBaseDir(),
	)
}

// Derived path helpers. All OCI data lives under {RootDir}/oci/.

func (c *Config) ociDir() string          { return filepath.Join(c.RootDir, "oci") }
func (c *Config) OCIDBDir() string        { return filepath.Join(c.ociDir(), "db") }
func (c *Config) OCITempDir() string      { return filepath.Join(c.ociDir(), "temp") }
func (c *Config) OCIBlobsDir() string     { return filepath.Join(c.ociDir(), "blobs") }
func (c *Config) OCIBootBaseDir() string  { return filepath.Join(c.ociDir(), "boot") }
func (c *Config) OCIIndexFile() string    { return filepath.Join(c.OCIDBDir(), "images.json") }
func (c *Config) OCIIndexLock() string    { return filepath.Join(c.OCIDBDir(), "images.lock") }

func (c *Config) BlobPath(layerDigestHex string) string {
	return filepath.Join(c.OCIBlobsDir(), layerDigestHex+".erofs")
}

func (c *Config) BootDir(layerDigestHex string) string {
	return filepath.Join(c.OCIBootBaseDir(), layerDigestHex)
}

func (c *Config) KernelPath(layerDigestHex string) string {
	return filepath.Join(c.BootDir(layerDigestHex), "vmlinuz")
}

func (c *Config) InitrdPath(layerDigestHex string) string {
	return filepath.Join(c.BootDir(layerDigestHex), "initrd.img")
}
