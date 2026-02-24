package utils

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const killWaitTimeout = 5 * time.Second

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

// VerifyProcess checks whether pid is running the expected binary.
// On Linux, reads /proc/{pid}/exe. Falls back to IsProcessAlive on other
// platforms or when /proc is unavailable.
func VerifyProcess(pid int, binaryName string) bool {
	if pid <= 0 {
		return false
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return IsProcessAlive(pid)
	}
	return filepath.Base(exe) == binaryName
}

// TerminateProcess sends SIGTERM to pid, waits up to gracePeriod for it to
// exit, then falls back to SIGKILL. Respects context cancellation during the
// grace period. Waits for the process to actually exit after SIGKILL.
func TerminateProcess(ctx context.Context, pid int, gracePeriod time.Duration) error {
	if !IsProcessAlive(pid) {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if !IsProcessAlive(pid) {
			return nil
		}
		return killAndWait(ctx, proc, pid)
	}

	// Wait for graceful exit.
	if err := WaitFor(ctx, gracePeriod, 100*time.Millisecond, func() (bool, error) { //nolint:mnd
		return !IsProcessAlive(pid), nil
	}); err == nil {
		return nil
	}

	// Escalate to SIGKILL.
	return killAndWait(ctx, proc, pid)
}

func killAndWait(ctx context.Context, proc *os.Process, pid int) error {
	_ = proc.Kill()
	return WaitFor(ctx, killWaitTimeout, 50*time.Millisecond, func() (bool, error) { //nolint:mnd
		return !IsProcessAlive(pid), nil
	})
}
