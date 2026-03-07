//go:build !linux

package utils

// ReflinkCopy copies a single file. On non-Linux, falls back to SparseCopy.
func ReflinkCopy(dst, src string) error {
	return SparseCopy(dst, src)
}
