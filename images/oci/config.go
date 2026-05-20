package oci

import (
	"path/filepath"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/images"
	"github.com/cocoonstack/cocoon/utils"
)

type Config struct {
	images.BaseConfig
}

func NewConfig(conf *config.Config) *Config {
	return &Config{BaseConfig: images.BaseConfig{
		Root: conf, Subdir: "oci", BlobExt: ".erofs",
	}}
}

func (c *Config) EnsureDirs() error {
	if err := c.EnsureBaseDirs(); err != nil {
		return err
	}
	return utils.EnsureDirs(c.BootBaseDir())
}

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
