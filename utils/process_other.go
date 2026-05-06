//go:build !linux

package utils

func verifyProcessCmdline(_ int, _, _ string) (matched, available bool) {
	return false, false
}
