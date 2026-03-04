package utils

import (
	"os"
	"strconv"
	"strings"
)

// DetectHugePages reads /proc/sys/vm/nr_hugepages and returns true
// if the host has hugepages configured (value > 0).
// Returns false on any error (non-Linux, file missing, etc.).
func DetectHugePages() bool {
	data, err := os.ReadFile("/proc/sys/vm/nr_hugepages")
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return err == nil && n > 0
}
