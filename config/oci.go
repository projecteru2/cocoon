package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureOCIDirs creates all required directories.
func (c *Config) EnsureOCIDirs() error {
	dirs := []string{
		c.DBDir(),
		c.TempDir(),
		c.BlobsDir(),
		c.BootBaseDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// Derived path helpers.

func (c *Config) DBDir() string          { return filepath.Join(c.RootDir, "db") }
func (c *Config) TempDir() string        { return filepath.Join(c.RootDir, "temp") }
func (c *Config) BlobsDir() string       { return filepath.Join(c.RootDir, "oci", "blobs") }
func (c *Config) BootBaseDir() string    { return filepath.Join(c.RootDir, "boot") }
func (c *Config) ImageIndexFile() string { return filepath.Join(c.RootDir, "db", "images.json") }
func (c *Config) ImageIndexLock() string { return filepath.Join(c.RootDir, "db", "images.lock") }

func (c *Config) BlobPath(layerDigestHex string) string {
	return filepath.Join(c.RootDir, "oci", "blobs", layerDigestHex+".erofs")
}

func (c *Config) BootDir(layerDigestHex string) string {
	return filepath.Join(c.RootDir, "boot", layerDigestHex)
}

func (c *Config) KernelPath(layerDigestHex string) string {
	return filepath.Join(c.BootDir(layerDigestHex), "vmlinuz")
}

func (c *Config) InitrdPath(layerDigestHex string) string {
	return filepath.Join(c.BootDir(layerDigestHex), "initrd.img")
}
