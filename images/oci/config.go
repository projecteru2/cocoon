package oci

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/utils"
)

// Config holds OCI image backend specific configuration, embedding the global config.
type Config struct {
	config.Config
}

// EnsureDirs creates all required directories for the OCI backend.
func (c *Config) EnsureDirs() error {
	return utils.EnsureDirs(
		c.DBDir(),
		c.TempDir(),
		c.BlobsDir(),
		c.BootBaseDir(),
	)
}

func (c *Config) DBDir() string       { return filepath.Join(c.dir(), "db") }
func (c *Config) TempDir() string     { return filepath.Join(c.dir(), "temp") }
func (c *Config) BlobsDir() string    { return filepath.Join(c.dir(), "blobs") }
func (c *Config) BootBaseDir() string { return filepath.Join(c.dir(), "boot") }
func (c *Config) IndexFile() string   { return filepath.Join(c.DBDir(), "images.json") }
func (c *Config) IndexLock() string   { return filepath.Join(c.DBDir(), "images.lock") }

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

func (c *Config) dir() string { return filepath.Join(c.RootDir, "oci") }
