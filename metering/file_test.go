package metering

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFileRecorderRoundTrip(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	r := NewFileRecorder(ctx, path)
	now := time.Now().UTC().Truncate(time.Microsecond)
	r.Emit(ctx, Entry{
		EmittedAt: now,
		Kind:      KindVMComputeStart,
		VMID:      "vm1",
		Reason:    ReasonBoot,
		Shape:     Shape{CPU: 4, MemBytes: 1 << 30},
	})
	r.Emit(ctx, Entry{
		EmittedAt: now.Add(time.Second),
		Kind:      KindVMComputeStop,
		VMID:      "vm1",
		Reason:    ReasonStopUser,
	})

	got := readEntries(t, path)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Kind != KindVMComputeStart || got[1].Kind != KindVMComputeStop {
		t.Errorf("got kinds %v %v", got[0].Kind, got[1].Kind)
	}
	if got[0].Shape.CPU != 4 {
		t.Errorf("got CPU=%d, want 4", got[0].Shape.CPU)
	}
}

func TestFileRecorderConcurrent(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	r := NewFileRecorder(ctx, path)
	const N = 200
	var wg sync.WaitGroup
	for i := range N {
		wg.Go(func() {
			r.Emit(ctx, Entry{Kind: KindVMComputeStart, VMID: "vm", Shape: Shape{CPU: i}})
		})
	}
	wg.Wait()

	got := readEntries(t, path)
	if len(got) != N {
		t.Errorf("got %d lines, want %d", len(got), N)
	}
}

func TestNewFileRecorderFallback(t *testing.T) {
	// Parent dir doesn't exist → OpenFile fails → fallback to NopRecorder.
	r := NewFileRecorder(t.Context(), filepath.Join(t.TempDir(), "missing-subdir", "ledger.jsonl"))
	if _, ok := r.(NopRecorder); !ok {
		t.Errorf("got %T, want NopRecorder", r)
	}
}

func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close() //nolint:errcheck

	var out []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}
