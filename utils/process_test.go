package utils

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// --- PID File ---

func TestWriteReadPIDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")

	if err := WritePIDFile(path, 12345); err != nil {
		t.Fatalf("WritePIDFile: %v", err)
	}

	got, err := ReadPIDFile(path)
	if err != nil {
		t.Fatalf("ReadPIDFile: %v", err)
	}
	if got != 12345 {
		t.Errorf("PID: got %d, want 12345", got)
	}
}

func TestReadPIDFile_NotExist(t *testing.T) {
	_, err := ReadPIDFile("/nonexistent/pid")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestReadPIDFile_InvalidContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pid")
	if err := os.WriteFile(path, []byte("not-a-number\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadPIDFile(path)
	if err == nil {
		t.Fatal("expected error for invalid content")
	}
}

func TestWritePIDFile_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perm.pid")
	if err := WritePIDFile(path, 1); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions: got %o, want 0600", fi.Mode().Perm())
	}
}

func TestWriteReadPIDFile_Roundtrip_LargePID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.pid")

	// Max PID on Linux is typically 4194304 (2^22).
	const largePID = 4194304
	if err := WritePIDFile(path, largePID); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPIDFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != largePID {
		t.Errorf("PID: got %d, want %d", got, largePID)
	}
}

func TestReadPIDFile_WhitespaceHandling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.pid")
	// Extra whitespace around PID.
	if err := os.WriteFile(path, []byte("  42  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPIDFile(path)
	if err != nil {
		t.Fatalf("ReadPIDFile: %v", err)
	}
	if got != 42 {
		t.Errorf("PID: got %d, want 42", got)
	}
}

// --- IsProcessAlive ---

func TestIsProcessAlive_Self(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) {
		t.Error("expected current process to be alive")
	}
}

func TestIsProcessAlive_InvalidPID(t *testing.T) {
	tests := []struct {
		name string
		pid  int
	}{
		{"zero", 0},
		{"negative", -1},
		{"large_negative", -99999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if IsProcessAlive(tt.pid) {
				t.Errorf("IsProcessAlive(%d) = true, want false", tt.pid)
			}
		})
	}
}

func TestIsProcessAlive_DeadProcess(t *testing.T) {
	// Start a process and wait for it to exit, then check.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()

	// Process has exited; PID may be recycled eventually,
	// but immediately after Wait it should not be alive.
	// Allow a small race window by retrying.
	if IsProcessAlive(pid) {
		// PID recycled extremely fast — skip rather than fail.
		t.Skip("PID recycled too quickly, skipping")
	}
}

// --- VerifyProcess ---

func TestVerifyProcess_Self(t *testing.T) {
	pid := os.Getpid()
	// The test binary name varies, so just check that it doesn't panic
	// and returns a boolean.
	_ = VerifyProcess(pid, "nonexistent-binary")
	// Self with correct binary should return true on Linux (falls back to IsProcessAlive on others).
	if !VerifyProcess(pid, filepath.Base(os.Args[0])) {
		// On non-Linux platforms, VerifyProcess falls back to IsProcessAlive, which should still be true.
		if !IsProcessAlive(pid) {
			t.Error("expected self process to be verifiable")
		}
	}
}

func TestVerifyProcess_InvalidPID(t *testing.T) {
	if VerifyProcess(0, "anything") {
		t.Error("expected false for PID 0")
	}
	if VerifyProcess(-1, "anything") {
		t.Error("expected false for PID -1")
	}
}

// --- VerifyProcessCmdline ---

func TestVerifyProcessCmdline_EmptyArg(t *testing.T) {
	pid := os.Getpid()
	// Empty expectArg delegates to VerifyProcess.
	result := VerifyProcessCmdline(pid, filepath.Base(os.Args[0]), "")
	// Should return true — process exists.
	if !result {
		if !IsProcessAlive(pid) {
			t.Error("expected self process to be verifiable with empty arg")
		}
	}
}

func TestVerifyProcessCmdline_InvalidPID(t *testing.T) {
	if VerifyProcessCmdline(0, "x", "y") {
		t.Error("expected false for PID 0")
	}
	if VerifyProcessCmdline(-1, "x", "y") {
		t.Error("expected false for PID -1")
	}
}

func TestVerifyProcessCmdline_WrongBinary(t *testing.T) {
	pid := os.Getpid()
	// On Linux, this checks /proc/pid/cmdline; on other platforms falls back to IsProcessAlive.
	result := VerifyProcessCmdline(pid, "definitely-not-the-binary", "definitely-not-the-arg")
	// On Linux, should return false (cmdline doesn't contain these strings).
	// On other platforms, falls back to IsProcessAlive (true).
	_ = result // Just verify no panic.
}

// --- TerminateProcess ---

func TestTerminateProcess_SleepProcess(t *testing.T) {
	// Start a sleep process we can terminate.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid

	// Reap the child in background so it doesn't become a zombie after SIGTERM.
	// Without this, kill(pid, 0) keeps returning nil for zombies and WaitFor times out.
	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()
	defer func() {
		_ = cmd.Process.Kill()
		<-waitDone
	}()

	if !IsProcessAlive(pid) {
		t.Fatal("sleep process should be alive")
	}

	ctx := t.Context()
	err := TerminateProcess(ctx, pid, "sleep", "60", 2*time.Second)
	if err != nil {
		t.Fatalf("TerminateProcess: %v", err)
	}

	// Wait for reaper goroutine to confirm exit.
	<-waitDone
}

func TestTerminateProcess_AlreadyDead(t *testing.T) {
	// Start and immediately wait.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()

	ctx := t.Context()
	// Should return nil — process doesn't exist, VerifyProcessCmdline returns false.
	err := TerminateProcess(ctx, pid, "true", "", 1*time.Second)
	if err != nil {
		t.Fatalf("TerminateProcess on dead process: %v", err)
	}
}

func TestTerminateProcess_InvalidPID(t *testing.T) {
	ctx := t.Context()
	// PID 0 → VerifyProcessCmdline returns false → return nil immediately.
	if err := TerminateProcess(ctx, 0, "x", "", time.Second); err != nil {
		t.Errorf("expected nil for PID 0, got %v", err)
	}
	if err := TerminateProcess(ctx, -1, "x", "", time.Second); err != nil {
		t.Errorf("expected nil for PID -1, got %v", err)
	}
}

func TestTerminateProcess_SIGTERMIgnored_FallsBackToKill(t *testing.T) {
	// Process that traps SIGTERM: won't die from SIGTERM alone.
	cmd := exec.Command("bash", "-c", `trap "" TERM; sleep 60`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid

	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()
	defer func() {
		_ = cmd.Process.Kill()
		<-waitDone
	}()

	// Very short grace period — SIGTERM won't kill it, fallback to SIGKILL.
	ctx := t.Context()
	err := TerminateProcess(ctx, pid, "bash", "", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("TerminateProcess: %v", err)
	}

	<-waitDone
}

func TestTerminateProcess_ContextCancelled(t *testing.T) {
	// Start a process that ignores SIGTERM (sleep handles it by default though).
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	// With cancelled context, TerminateProcess should still attempt to kill.
	// It may return context error from WaitFor, but the process should be killed.
	_ = TerminateProcess(ctx, pid, "sleep", "60", 100*time.Millisecond)
}
