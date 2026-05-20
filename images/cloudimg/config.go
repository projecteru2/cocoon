package cloudimg

import (
	"path/filepath"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/images"
)

type Config struct {
	images.BaseConfig
}

func NewConfig(conf *config.Config) *Config {
	return &Config{BaseConfig: images.BaseConfig{
		Root: conf, Subdir: "cloudimg", BlobExt: ".qcow2",
	}}
}

func (c *Config) EnsureDirs() error {
	return c.EnsureBaseDirs()
}

// FirmwarePath returns the UEFI firmware blob (CLOUDHV.fd) under conf.RootDir/firmware.
func (c *Config) FirmwarePath() string {
	return filepath.Join(c.Root.RootDir, "firmware", "CLOUDHV.fd")
}

// tmpBlobPath uses a hidden prefix so a partial write is safe under last-writer-wins.
func (c *Config) tmpBlobPath(digestHex string) string {
	return filepath.Join(c.TempDir(), ".tmp-"+digestHex+".qcow2")
}
