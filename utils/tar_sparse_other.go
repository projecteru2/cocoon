//go:build !linux

package utils

import "archive/tar"

// tarFileMaybeSparse falls back to regular TarFile on non-Linux platforms
// where SEEK_HOLE/SEEK_DATA is not available.
func tarFileMaybeSparse(tw *tar.Writer, path, nameInTar string) error {
	return TarFile(tw, path, nameInTar)
}
