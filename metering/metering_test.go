package metering

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEntryJSONRoundTrip(t *testing.T) {
	in := Entry{
		EmittedAt:  time.Date(2026, 5, 20, 1, 2, 3, 0, time.UTC),
		Kind:       KindVMComputeStart,
		VMID:       "vm1",
		Reason:     ReasonBoot,
		Hypervisor: "ch",
		Shape:      Shape{CPU: 4, MemBytes: 1 << 30, StorageBytes: 10 << 30},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Entry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip diverged:\n got: %#v\nwant: %#v", out, in)
	}
}

func TestKindWireFormat(t *testing.T) {
	// Wire-format strings are consumed by external BQ schema; renaming any
	// of these is a breaking change for downstream consumers.
	cases := []struct {
		got, want string
	}{
		{string(KindVMComputeStart), "vm.compute.start"},
		{string(KindVMComputeStop), "vm.compute.stop"},
		{string(KindVMStorageStart), "vm.storage.start"},
		{string(KindVMStorageStop), "vm.storage.stop"},
		{string(KindSnapStorageStart), "snap.storage.start"},
		{string(KindSnapStorageStop), "snap.storage.stop"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestNopRecorder(t *testing.T) {
	var r NopRecorder
	r.Emit(t.Context(), Entry{Kind: KindVMComputeStart, VMID: "x"})
	// no panic, no state — only assertion is "does not crash"
}
