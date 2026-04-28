package localfile

import (
	"path/filepath"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/utils"
)

// Config holds localfile snapshot backend configuration, embedding the global config.
type Config struct {
	*config.Config
}

func NewConfig(conf *config.Config) *Config {
	return &Config{Config: conf}
}

func (c *Config) EnsureDirs() error {
	return utils.EnsureDirs(
		c.dbDir(),
		c.DataDir(),
	)
}

func (c *Config) DataDir() string { return filepath.Join(c.dir(), "localfile") }

func (c *Config) SnapshotDataDir(id string) string { return filepath.Join(c.DataDir(), id) }

func (c *Config) IndexFile() string { return filepath.Join(c.dbDir(), "snapshots.json") }

func (c *Config) IndexLock() string { return filepath.Join(c.dbDir(), "snapshots.lock") }

func (c *Config) dir() string   { return filepath.Join(c.RootDir, "snapshot") }
func (c *Config) dbDir() string { return filepath.Join(c.dir(), "db") }
