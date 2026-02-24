package config

import (
	"encoding/json"
	"fmt"
	"os"

	"runtime"

	coretypes "github.com/projecteru2/core/types"
)

// Config holds global Cocoon configuration.
type Config struct {
	// RootDir is the base directory for persistent data (images, firmware, VM DB).
	// Env: COCOON_ROOT_DIR. Default: /var/lib/cocoon.
	RootDir string `json:"root_dir"`
	// RunDir is the base directory for runtime state (PID files, Unix sockets).
	// Contents are ephemeral and may not survive reboots.
	// Env: COCOON_RUN_DIR. Default: /var/run/cocoon.
	RunDir string `json:"run_dir"`
	// LogDir is the base directory for VM and process logs.
	// Env: COCOON_LOG_DIR. Default: /var/log/cocoon.
	LogDir string `json:"log_dir"`
	// CHBinary is the path or name of the cloud-hypervisor executable.
	// Default: "cloud-hypervisor".
	CHBinary string `json:"ch_binary"`
	// StopTimeoutSeconds is how long to wait for a guest to respond to an
	// ACPI power-button before falling back to SIGTERM/SIGKILL.
	// Default: 30.
	StopTimeoutSeconds int `json:"stop_timeout_seconds"`
	// PoolSize is the goroutine pool size for concurrent operations.
	// Defaults to runtime.NumCPU() if zero.
	PoolSize int `json:"pool_size"`
	// Log configuration, uses eru core's ServerLogConfig.
	Log coretypes.ServerLogConfig `json:"log"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		RootDir:            "/var/lib/cocoon",
		RunDir:             "/var/run/cocoon",
		LogDir:             "/var/log/cocoon",
		CHBinary:           "cloud-hypervisor",
		StopTimeoutSeconds: 30,
		PoolSize:           runtime.NumCPU(),
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

