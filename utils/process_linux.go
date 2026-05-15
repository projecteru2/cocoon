//go:build linux

package utils

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// FindVMMByCmdline returns pids whose argv[0] basename matches binaryName and args contain expectArg, sorted numerically; fails closed on non-ENOENT cmdline read errors.
func FindVMMByCmdline(binaryName, expectArg string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	var firstErr error
	for _, e := range entries {
		pid, atoiErr := strconv.Atoi(e.Name())
		if atoiErr != nil || pid <= 0 {
			continue
		}
		matched, readErr := verifyProcessCmdline(pid, binaryName, expectArg)
		if readErr != nil {
			// ENOENT = process exited mid-scan, safe to skip; everything else means we can't tell, so callers must fail closed.
			if !errors.Is(readErr, fs.ErrNotExist) && firstErr == nil {
				firstErr = fmt.Errorf("read /proc/%d/cmdline: %w", pid, readErr)
			}
			continue
		}
		if matched {
			pids = append(pids, pid)
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	slices.Sort(pids)
	return pids, nil
}

// Match argv[0] basename strictly + expectArg substring on the rest so "bash -c 'cloud-hypervisor ...'" can't impersonate the VMM; error surfaces cmdline-read failures so callers distinguish transient ENOENT from real issues.
func verifyProcessCmdline(pid int, binaryName, expectArg string) (bool, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false, err
	}
	argv0, rest, _ := strings.Cut(string(data), "\x00")
	if filepath.Base(argv0) != binaryName {
		return false, nil
	}
	if expectArg == "" {
		return true, nil
	}
	return strings.Contains(rest, expectArg), nil
}
