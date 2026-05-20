package images

import (
	"path/filepath"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/utils"
)

// BaseConfig is the directory layout shared by all image backends.
type BaseConfig struct {
	Root    *config.Config
	Subdir  string
	BlobExt string
}

func (c *BaseConfig) BackendDir() string { return filepath.Join(c.Root.RootDir, c.Subdir) }
func (c *BaseConfig) DBDir() string      { return filepath.Join(c.BackendDir(), "db") }
func (c *BaseConfig) TempDir() string    { return filepath.Join(c.BackendDir(), "temp") }
func (c *BaseConfig) BlobsDir() string   { return filepath.Join(c.BackendDir(), "blobs") }
func (c *BaseConfig) IndexFile() string  { return filepath.Join(c.DBDir(), "images.json") }
func (c *BaseConfig) IndexLock() string  { return filepath.Join(c.DBDir(), "images.lock") }

func (c *BaseConfig) BlobPath(hex string) string {
	return filepath.Join(c.BlobsDir(), hex+c.BlobExt)
}

func (c *BaseConfig) EnsureBaseDirs() error {
	return utils.EnsureDirs(c.DBDir(), c.TempDir(), c.BlobsDir())
}
