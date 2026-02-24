package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	coretypes "github.com/projecteru2/core/types"
)

// Config holds global Cocoon configuration.
type Config struct {
	// RootDir is the base directory for persistent data.
	RootDir string `json:"root_dir"`
	// PoolSize is the goroutine pool size for concurrent operations.
	// Defaults to runtime.NumCPU() if zero.
	PoolSize int `json:"pool_size"`
	// Log configuration, uses eru core's ServerLogConfig.
	Log coretypes.ServerLogConfig `json:"log"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		RootDir:  "/var/lib/cocoon",
		PoolSize: runtime.NumCPU(),
		Log: coretypes.ServerLogConfig{
			Level:      "info",
			MaxSize:    500,
			MaxAge:     28,
			MaxBackups: 3,
		},
	}
}

// LoadConfig loads configuration from file, falling back to defaults.
func LoadConfig(path string) (*Config, error) {
	conf := DefaultConfig()
	if path == "" {
		return conf, nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // config path from CLI flag
	if err != nil {
		if os.IsNotExist(err) {
			return conf, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(data, conf); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if conf.PoolSize <= 0 {
		conf.PoolSize = runtime.NumCPU()
	}
	return conf, nil
}

// FirmwarePath returns the path to the UEFI firmware file (CLOUDHV.fd).
func (c *Config) FirmwarePath() string {
	return filepath.Join(c.RootDir, "firmware", "CLOUDHV.fd")
}
