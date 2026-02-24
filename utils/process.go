package utils

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// WritePIDFile writes pid to path with 0600 permissions.
func WritePIDFile(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

// ReadPIDFile reads a PID integer from path.
func ReadPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // internal runtime path
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse PID from %s: %w", path, err)
	}
	return pid, nil
}

// IsProcessAlive returns true if a process with the given PID currently exists.
// Uses kill(pid, 0) — no signal is sent, only existence is checked.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// TerminateProcess sends SIGTERM to pid, waits up to gracePeriod for it to
// exit, then falls back to SIGKILL.
func TerminateProcess(pid int, gracePeriod time.Duration) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil // already gone
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if !IsProcessAlive(pid) {
			return nil
		}
		return proc.Kill()
	}
	deadline := time.Now().Add(gracePeriod)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return proc.Kill()
}
