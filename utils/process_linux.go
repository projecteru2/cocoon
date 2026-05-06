//go:build linux

package utils

import (
	"fmt"
	"os"
	"strings"
)

func verifyProcessCmdline(pid int, binaryName, expectArg string) (matched, available bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false, false
	}
	cmdline := string(data)
	return strings.Contains(cmdline, binaryName) && strings.Contains(cmdline, expectArg), true
}
