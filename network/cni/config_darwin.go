package cni

import "errors"

var errNotSupported = errors.New("network namespace operations are not supported on darwin")

func createNetns(_ string) error {
	return errNotSupported
}

func deleteNetns(_ string) error {
	return errNotSupported
}

func setupTCRedirect(_, _, _ string) error {
	return errNotSupported
}
