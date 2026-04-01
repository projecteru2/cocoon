package images

import (
	"path/filepath"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/utils"
)

// BaseConfig holds the common directory layout shared by all image backends.
// Each backend embeds BaseConfig and adds type-specific paths.
type BaseConfig struct {
	Root    *config.Config
	Subdir  string // backend subdirectory under RootDir, e.g. "oci" or "cloudimg"
	BlobExt string // blob file extension, e.g. ".erofs" or ".qcow2"
}

// BackendDir returns the root directory for this image backend.
func (c *BaseConfig) BackendDir() string { return filepath.Join(c.Root.RootDir, c.Subdir) }
func (c *BaseConfig) DBDir() string      { return filepath.Join(c.BackendDir(), "db") }
func (c *BaseConfig) TempDir() string    { return filepath.Join(c.BackendDir(), "temp") }
func (c *BaseConfig) BlobsDir() string   { return filepath.Join(c.BackendDir(), "blobs") }
func (c *BaseConfig) IndexFile() string  { return filepath.Join(c.DBDir(), "images.json") }
func (c *BaseConfig) IndexLock() string  { return filepath.Join(c.DBDir(), "images.lock") }

// BlobPath returns the full path for a blob with the given digest hex.
func (c *BaseConfig) BlobPath(hex string) string {
	return filepath.Join(c.BlobsDir(), hex+c.BlobExt)
}

// EnsureBaseDirs creates the common directories (db, temp, blobs).
func (c *BaseConfig) EnsureBaseDirs() error {
	return utils.EnsureDirs(c.DBDir(), c.TempDir(), c.BlobsDir())
}
