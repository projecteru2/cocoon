package cloudimg

import (
	"path/filepath"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/images"
)

// Config holds cloud image backend specific configuration, embedding the shared BaseConfig.
type Config struct {
	images.BaseConfig
}

// NewConfig creates a Config from a global config.
func NewConfig(conf *config.Config) *Config {
	return &Config{BaseConfig: images.BaseConfig{
		Root: conf, Subdir: "cloudimg", BlobExt: ".qcow2",
	}}
}

// EnsureDirs creates all required directories for the cloudimg backend.
func (c *Config) EnsureDirs() error {
	return c.EnsureBaseDirs()
}

// FirmwarePath returns the path to the UEFI firmware blob (CLOUDHV.fd).
func (c *Config) FirmwarePath() string {
	return filepath.Join(c.Root.RootDir, "firmware", "CLOUDHV.fd")
}

// tmpBlobPath returns the digest-derived intermediate temp blob path
// used by both the fast-path rename and the slow-path convert target
// in commit.go. Naming lives in one place so the collision-benign
// reasoning (identical digest → identical content → safe
// last-writer-wins) applies uniformly across both paths.
func (c *Config) tmpBlobPath(digestHex string) string {
	return filepath.Join(c.TempDir(), ".tmp-"+digestHex+".qcow2")
}
