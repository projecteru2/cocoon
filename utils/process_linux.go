//go:build linux

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// /proc/<pid>/cmdline is NUL-separated argv. Match argv[0] basename strictly,
// then apply the optional expectArg substring check on the remainder so a
// process running "bash -c 'cloud-hypervisor ...'" can't impersonate the VMM.
func verifyProcessCmdline(pid int, binaryName, expectArg string) (matched, available bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false, false
	}
	argv0, rest, _ := strings.Cut(string(data), "\x00")
	if filepath.Base(argv0) != binaryName {
		return false, true
	}
	if expectArg == "" {
		return true, true
	}
	return strings.Contains(rest, expectArg), true
}
