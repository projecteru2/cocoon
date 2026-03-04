//go:build !linux

package utils

func verifyProcessExe(_ int, _ string) (matched, available bool) {
	return false, false
}

func verifyProcessCmdline(_ int, _, _ string) (matched, available bool) {
	return false, false
}
