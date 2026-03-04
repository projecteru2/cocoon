//go:build linux

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func verifyProcessExe(pid int, binaryName string) (matched, available bool) {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return false, false
	}
	return filepath.Base(exe) == binaryName, true
}

func verifyProcessCmdline(pid int, binaryName, expectArg string) (matched, available bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false, false
	}
	cmdline := string(data)
	return strings.Contains(cmdline, binaryName) && strings.Contains(cmdline, expectArg), true
}
