package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureOCIDirs creates all required OCI-related directories.
func (c *Config) EnsureOCIDirs() error {
	dirs := []string{
		c.DBDir(),
		c.TempDir(),
		c.OCIDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// Derived path helpers.

func (c *Config) DBDir() string {
	return filepath.Join(c.RootDir, "db")
}

func (c *Config) TempDir() string {
	return filepath.Join(c.RootDir, "temp")
}

func (c *Config) OCIDir() string {
	return filepath.Join(c.RootDir, "oci")
}

func (c *Config) ImageDir(digest string) string {
	return filepath.Join(c.RootDir, "oci", digest)
}

func (c *Config) BootDir(layerDigest string) string {
	return filepath.Join(c.RootDir, "boot", layerDigest)
}

func (c *Config) ImageIndexFile() string {
	return filepath.Join(c.RootDir, "db", "images.json")
}

func (c *Config) ImageIndexLock() string {
	return filepath.Join(c.RootDir, "db", "images.lock")
}
