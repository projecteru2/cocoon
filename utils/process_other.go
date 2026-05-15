//go:build !linux

package utils

func FindVMMByCmdline(_, _ string) ([]int, error) {
	return nil, nil
}

func verifyProcessCmdline(_ int, _, _ string) (matched, available bool) {
	return false, false
}
