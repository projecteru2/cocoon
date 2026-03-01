package config

import (
	"strings"

	coretypes "github.com/projecteru2/core/types"
)

// Config holds global Cocoon configuration.
type Config struct {
	// RootDir is the base directory for persistent data (images, firmware, VM DB).
	// Env: COCOON_ROOT_DIR. Default: /var/lib/cocoon.
	RootDir string `json:"root_dir" mapstructure:"root_dir"`
	// RunDir is the base directory for runtime state (PID files, Unix sockets).
	// Contents are ephemeral and may not survive reboots.
	// Env: COCOON_RUN_DIR. Default: /var/lib/cocoon/run.
	RunDir string `json:"run_dir" mapstructure:"run_dir"`
	// LogDir is the base directory for VM and process logs.
	// Env: COCOON_LOG_DIR. Default: /var/log/cocoon.
	LogDir string `json:"log_dir" mapstructure:"log_dir"`
	// CHBinary is the path or name of the cloud-hypervisor executable.
	// Default: "cloud-hypervisor".
	CHBinary string `json:"ch_binary" mapstructure:"ch_binary"`
	// StopTimeoutSeconds is how long to wait for a guest to respond to an
	// ACPI power-button before falling back to SIGTERM/SIGKILL.
	// Default: 30.
	StopTimeoutSeconds int `json:"stop_timeout_seconds" mapstructure:"stop_timeout_seconds"`
	// PoolSize is the goroutine pool size for concurrent operations.
	// Defaults to runtime.NumCPU() if zero.
	PoolSize int `json:"pool_size" mapstructure:"pool_size"`
	// CNIConfDir is the directory for CNI plugin configuration files.
	// Default: /etc/cni/net.d.
	CNIConfDir string `json:"cni_conf_dir" mapstructure:"cni_conf_dir"`
	// CNIBinDir is the directory for CNI plugin binaries.
	// Default: /opt/cni/bin.
	CNIBinDir string `json:"cni_bin_dir" mapstructure:"cni_bin_dir"`
	// DefaultRootPassword is the root password injected into cloudimg VMs
	// via cloud-init metadata. Empty means no password is set.
	DefaultRootPassword string `json:"default_root_password" mapstructure:"default_root_password"`
	// DNS is a comma or semicolon separated list of DNS server addresses
	// injected into VM network configuration.
	// Env: COCOON_DNS. Default: "8.8.8.8,1.1.1.1".
	DNS string `json:"dns" mapstructure:"dns"`
	// Log configuration, uses eru core's ServerLogConfig.
	Log *coretypes.ServerLogConfig `json:"log" mapstructure:"log"`
}

// DNSServers parses the DNS string into a slice of server addresses.
func (c *Config) DNSServers() []string {
	if c.DNS == "" {
		return nil
	}
	raw := strings.ReplaceAll(c.DNS, ";", ",")
	var servers []string
	for s := range strings.SplitSeq(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			servers = append(servers, s)
		}
	}
	return servers
}
