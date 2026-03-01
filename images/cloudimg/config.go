package cloudimg

import (
	"path/filepath"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/utils"
)

// Config holds cloud image backend specific configuration, embedding the global config.
type Config struct {
	config.Config
}

// NewConfig creates a Config from a global config (value copy).
func NewConfig(conf *config.Config) *Config {
	if conf == nil {
		return &Config{}
	}
	return &Config{Config: *conf}
}

// EnsureDirs creates all required directories for the cloudimg backend.
func (c *Config) EnsureDirs() error {
	return utils.EnsureDirs(
		c.DBDir(),
		c.TempDir(),
		c.BlobsDir(),
	)
}

func (c *Config) DBDir() string     { return filepath.Join(c.dir(), "db") }
func (c *Config) TempDir() string   { return filepath.Join(c.dir(), "temp") }
func (c *Config) BlobsDir() string  { return filepath.Join(c.dir(), "blobs") }
func (c *Config) IndexFile() string { return filepath.Join(c.DBDir(), "images.json") }
func (c *Config) IndexLock() string { return filepath.Join(c.DBDir(), "images.lock") }

func (c *Config) BlobPath(hex string) string {
	return filepath.Join(c.BlobsDir(), hex+".qcow2")
}

// FirmwarePath returns the path to the UEFI firmware blob (CLOUDHV.fd).
func (c *Config) FirmwarePath() string {
	return filepath.Join(c.RootDir, "firmware", "CLOUDHV.fd")
}

func (c *Config) dir() string { return filepath.Join(c.RootDir, "cloudimg") }
