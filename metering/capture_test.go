package metering

import (
	"sync"
	"testing"
)

func TestCaptureRecorderBasic(t *testing.T) {
	var r CaptureRecorder
	ctx := t.Context()
	r.Emit(ctx, Entry{Kind: KindVMComputeStart, VMID: "a"})
	r.Emit(ctx, Entry{Kind: KindVMComputeStop, VMID: "a"})
	got := r.Entries()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Kind != KindVMComputeStart || got[1].Kind != KindVMComputeStop {
		t.Errorf("got kinds %v %v", got[0].Kind, got[1].Kind)
	}
}

func TestCaptureRecorderEntriesIsCopy(t *testing.T) {
	// Mutating the returned slice must not affect subsequent reads.
	var r CaptureRecorder
	r.Emit(t.Context(), Entry{VMID: "a"})
	got := r.Entries()
	got[0].VMID = "tampered"
	if again := r.Entries(); again[0].VMID != "a" {
		t.Errorf("Entries() must return a copy; got %q after mutation", again[0].VMID)
	}
}

func TestCaptureRecorderConcurrent(t *testing.T) {
	var r CaptureRecorder
	ctx := t.Context()
	const N = 200
	var wg sync.WaitGroup
	for range N {
		wg.Go(func() {
			r.Emit(ctx, Entry{Kind: KindVMComputeStart, VMID: "vm"})
		})
	}
	wg.Wait()
	if got := len(r.Entries()); got != N {
		t.Errorf("got %d entries, want %d", got, N)
	}
}
