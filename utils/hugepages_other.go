//go:build !linux

package utils

// DetectHugePages returns false on non-Linux platforms where /proc/sys/vm/nr_hugepages is not available.
func DetectHugePages() bool { return false }
