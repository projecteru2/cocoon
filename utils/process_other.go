//go:build !linux

package utils

import "errors"

var errVerifyUnsupported = errors.New("verifyProcessCmdline: unsupported on this OS")

func FindVMMByCmdline(_, _ string) ([]int, error) {
	return nil, nil
}

func verifyProcessCmdline(_ int, _, _ string) (bool, error) {
	return false, errVerifyUnsupported
}
