package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cocoonstack/cocoon/types"
)

func TestReadSnapshotEnvelope_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := types.SnapshotConfig{
		ID:         "snap-xxx",
		Name:       "demo",
		Hypervisor: "cloud-hypervisor",
		NICs:       1,
	}
	if err := WriteSnapshotEnvelope(dir, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadSnapshotEnvelope(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ID != cfg.ID || got.Name != cfg.Name || got.Hypervisor != cfg.Hypervisor || got.NICs != cfg.NICs {
		t.Errorf("got %+v, want %+v", got, cfg)
	}
}

func TestReadSnapshotEnvelope_Missing(t *testing.T) {
	_, err := ReadSnapshotEnvelope(t.TempDir())
	if !errors.Is(err, ErrEnvelopeMissing) {
		t.Errorf("got %v, want wrap of ErrEnvelopeMissing", err)
	}
}

func TestReadSnapshotEnvelope_BadVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SnapshotJSONName),
		[]byte(`{"version": 999, "config": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadSnapshotEnvelope(dir)
	if err == nil {
		t.Fatal("want version error")
	}
}

func TestReadSnapshotEnvelope_BadJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SnapshotJSONName),
		[]byte(`{ not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadSnapshotEnvelope(dir)
	if err == nil {
		t.Fatal("want parse error")
	}
}
