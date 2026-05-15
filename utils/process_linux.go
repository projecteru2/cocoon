//go:build linux

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FindVMMByCmdline returns pids whose argv[0] basename matches binaryName and args contain expectArg.
func FindVMMByCmdline(binaryName, expectArg string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if matched, _ := verifyProcessCmdline(pid, binaryName, expectArg); matched {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// Match argv[0] basename strictly + expectArg substring on the rest so "bash -c 'cloud-hypervisor ...'" can't impersonate the VMM.
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
