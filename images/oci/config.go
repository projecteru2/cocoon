package oci

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/utils"
)

// Config holds OCI image backend specific configuration, embedding the shared BaseConfig.
type Config struct {
	images.BaseConfig
}

// NewConfig creates an OCI Config from a global config.
func NewConfig(conf *config.Config) *Config {
	return &Config{BaseConfig: images.BaseConfig{
		Root: conf, Subdir: "oci", BlobExt: ".erofs",
	}}
}

// EnsureDirs creates all required directories for the OCI backend.
func (c *Config) EnsureDirs() error {
	if err := c.EnsureBaseDirs(); err != nil {
		return err
	}
	return utils.EnsureDirs(c.BootBaseDir())
}

// OCI-specific paths.

func (c *Config) BootBaseDir() string { return filepath.Join(c.BackendDir(), "boot") }

func (c *Config) BootDir(layerDigestHex string) string {
	return filepath.Join(c.BootBaseDir(), layerDigestHex)
}

func (c *Config) KernelPath(layerDigestHex string) string {
	return filepath.Join(c.BootDir(layerDigestHex), "vmlinuz")
}

func (c *Config) InitrdPath(layerDigestHex string) string {
	return filepath.Join(c.BootDir(layerDigestHex), "initrd.img")
}
