package localfile

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/snapshot"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// helpers

// testID generates a random snapshot ID for tests.
func testID(t *testing.T) string {
	t.Helper()
	id, err := utils.GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	return id
}

// newTestLF creates a LocalFile backed by a temp directory.
func newTestLF(t *testing.T) *LocalFile {
	t.Helper()
	dir := t.TempDir()
	lf, err := New(&config.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return lf
}

// makeTarGz builds a tar.gz archive in memory from a map of name→content.
func makeTarGz(t *testing.T, files map[string][]byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Size:     int64(len(data)),
			Mode:     0o644,
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	return &buf
}

// New

func TestNew(t *testing.T) {
	dir := t.TempDir()
	lf, err := New(&config.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if lf == nil {
		t.Fatal("expected non-nil LocalFile")
	}
}

func TestNew_NilConfig(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

// Create

func TestCreate(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{
		"cow.raw":    []byte("disk data"),
		"state.json": []byte(`{"state":"ok"}`),
	})

	cfg := &types.SnapshotConfig{
		ID:          testID(t),
		Name:        "snap1",
		Description: "test snapshot",
		ImageBlobIDs: map[string]struct{}{
			"abc123": {},
		},
	}

	id, err := lf.Create(ctx, cfg, stream)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	// Verify data files were extracted.
	dataDir := lf.conf.SnapshotDataDir(id)
	for _, name := range []string{"cow.raw", "state.json"} {
		if _, err := os.Stat(filepath.Join(dataDir, name)); err != nil {
			t.Errorf("expected %s in data dir: %v", name, err)
		}
	}
}

func TestCreate_NoName(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{"f.txt": []byte("x")})
	id, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t)}, stream)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}
}

func TestCreate_DuplicateName(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	cfg := &types.SnapshotConfig{ID: testID(t), Name: "dup"}

	stream1 := makeTarGz(t, map[string][]byte{"a.txt": []byte("a")})
	if _, err := lf.Create(ctx, cfg, stream1); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	cfg2 := &types.SnapshotConfig{ID: testID(t), Name: "dup"}
	stream2 := makeTarGz(t, map[string][]byte{"b.txt": []byte("b")})
	_, err := lf.Create(ctx, cfg2, stream2)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreate_InvalidStream(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	_, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "bad"}, strings.NewReader("not gzip"))
	if err == nil {
		t.Fatal("expected error for invalid stream")
	}
}

// List

func TestList_Empty(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	result, err := lf.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(result))
	}
}

func TestList(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	for _, name := range []string{"s1", "s2", "s3"} {
		stream := makeTarGz(t, map[string][]byte{"f.txt": []byte(name)})
		if _, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: name}, stream); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	result, err := lf.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 snapshots, got %d", len(result))
	}

	names := make(map[string]bool)
	for _, s := range result {
		names[s.Name] = true
	}
	for _, name := range []string{"s1", "s2", "s3"} {
		if !names[name] {
			t.Errorf("missing snapshot %q", name)
		}
	}
}

// Inspect

func TestInspect_ByID(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{"f.txt": []byte("x")})
	id, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "byid", Description: "desc"}, stream)
	if err != nil {
		t.Fatal(err)
	}

	s, err := lf.Inspect(ctx, id)
	if err != nil {
		t.Fatalf("Inspect by ID: %v", err)
	}
	if s.ID != id {
		t.Errorf("ID: got %q, want %q", s.ID, id)
	}
	if s.Name != "byid" {
		t.Errorf("Name: got %q, want %q", s.Name, "byid")
	}
	if s.Description != "desc" {
		t.Errorf("Description: got %q, want %q", s.Description, "desc")
	}
}

func TestInspect_ByName(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{"f.txt": []byte("x")})
	id, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "byname"}, stream)
	if err != nil {
		t.Fatal(err)
	}

	s, err := lf.Inspect(ctx, "byname")
	if err != nil {
		t.Fatalf("Inspect by name: %v", err)
	}
	if s.ID != id {
		t.Errorf("ID: got %q, want %q", s.ID, id)
	}
}

func TestInspect_ByPrefix(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{"f.txt": []byte("x")})
	id, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "pfx"}, stream)
	if err != nil {
		t.Fatal(err)
	}

	// Use first 5 chars as prefix (IDs are 16-char hex).
	prefix := id[:5]
	s, err := lf.Inspect(ctx, prefix)
	if err != nil {
		t.Fatalf("Inspect by prefix %q: %v", prefix, err)
	}
	if s.ID != id {
		t.Errorf("ID: got %q, want %q", s.ID, id)
	}
}

func TestInspect_NotFound(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	_, err := lf.Inspect(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, snapshot.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// Delete

func TestDelete(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{"f.txt": []byte("x")})
	id, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "del"}, stream)
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := lf.Delete(ctx, []string{"del"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != id {
		t.Errorf("deleted: got %v, want [%s]", deleted, id)
	}

	// Data dir should be gone.
	if _, err := os.Stat(lf.conf.SnapshotDataDir(id)); !os.IsNotExist(err) {
		t.Error("expected data dir to be removed")
	}

	// Inspect should fail.
	if _, err := lf.Inspect(ctx, id); !errors.Is(err, snapshot.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}

	// List should be empty.
	list, _ := lf.List(ctx)
	if len(list) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(list))
	}
}

func TestDelete_ByID(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{"f.txt": []byte("x")})
	id, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "delid"}, stream)
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := lf.Delete(ctx, []string{id})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != id {
		t.Errorf("deleted: got %v, want [%s]", deleted, id)
	}
}

func TestDelete_Multiple(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	var ids []string
	for _, name := range []string{"m1", "m2", "m3"} {
		stream := makeTarGz(t, map[string][]byte{"f.txt": []byte(name)})
		id, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: name}, stream)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	deleted, err := lf.Delete(ctx, []string{"m1", "m3"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deleted) != 2 {
		t.Errorf("expected 2 deleted, got %d", len(deleted))
	}

	// m2 should still exist.
	list, _ := lf.List(ctx)
	if len(list) != 1 || list[0].Name != "m2" {
		t.Errorf("expected only m2 remaining, got %v", list)
	}
}

func TestDelete_DuplicateRefs(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{"f.txt": []byte("x")})
	id, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "dedup"}, stream)
	if err != nil {
		t.Fatal(err)
	}

	// Pass the same ref twice — should deduplicate.
	deleted, err := lf.Delete(ctx, []string{id, "dedup"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deleted) != 1 {
		t.Errorf("expected 1 deleted (deduped), got %d", len(deleted))
	}
}

func TestDelete_NotFound(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	_, err := lf.Delete(ctx, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent ref")
	}
}

// Create → Inspect round trip verifies timestamps and fields.

func TestCreate_Inspect_Fields(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{"cow.raw": []byte("data")})
	cfg := &types.SnapshotConfig{
		ID:           testID(t),
		Name:         "fields",
		Description:  "full field check",
		ImageBlobIDs: map[string]struct{}{"hex1": {}, "hex2": {}},
	}

	id, err := lf.Create(ctx, cfg, stream)
	if err != nil {
		t.Fatal(err)
	}

	s, err := lf.Inspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != id {
		t.Errorf("ID mismatch")
	}
	if s.Name != "fields" {
		t.Errorf("Name: got %q", s.Name)
	}
	if s.Description != "full field check" {
		t.Errorf("Description: got %q", s.Description)
	}
	if s.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

// Delete then recreate with same name should succeed.

func TestDelete_RecreateName(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream1 := makeTarGz(t, map[string][]byte{"f.txt": []byte("v1")})
	_, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "reuse"}, stream1)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := lf.Delete(ctx, []string{"reuse"}); err != nil {
		t.Fatal(err)
	}

	stream2 := makeTarGz(t, map[string][]byte{"f.txt": []byte("v2")})
	id2, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "reuse"}, stream2)
	if err != nil {
		t.Fatalf("recreate with same name: %v", err)
	}

	s, err := lf.Inspect(ctx, "reuse")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != id2 {
		t.Errorf("expected new ID %q, got %q", id2, s.ID)
	}
}

// Restore

func TestRestore_ConfigRoundtrip(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	stream := makeTarGz(t, map[string][]byte{"cow.raw": []byte("disk")})
	cfg := &types.SnapshotConfig{
		ID:           testID(t),
		Name:         "rt",
		Description:  "roundtrip",
		Image:        "ubuntu:22.04",
		ImageBlobIDs: map[string]struct{}{"deadbeef": {}},
		CPU:          4,
		Memory:       1 << 30, // 1 GiB
		Storage:      10 << 30,
		NICs:         2,
	}

	id, err := lf.Create(ctx, cfg, stream)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, rc, err := lf.Restore(ctx, id)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	rc.Close()

	if got.Name != cfg.Name {
		t.Errorf("Name: got %q, want %q", got.Name, cfg.Name)
	}
	if got.Description != cfg.Description {
		t.Errorf("Description: got %q, want %q", got.Description, cfg.Description)
	}
	if got.Image != cfg.Image {
		t.Errorf("Image: got %q, want %q", got.Image, cfg.Image)
	}
	if _, ok := got.ImageBlobIDs["deadbeef"]; !ok {
		t.Errorf("ImageBlobIDs missing 'deadbeef': %v", got.ImageBlobIDs)
	}
	if got.CPU != cfg.CPU {
		t.Errorf("CPU: got %d, want %d", got.CPU, cfg.CPU)
	}
	if got.Memory != cfg.Memory {
		t.Errorf("Memory: got %d, want %d", got.Memory, cfg.Memory)
	}
	if got.Storage != cfg.Storage {
		t.Errorf("Storage: got %d, want %d", got.Storage, cfg.Storage)
	}
	if got.NICs != cfg.NICs {
		t.Errorf("NICs: got %d, want %d", got.NICs, cfg.NICs)
	}
}

func TestRestore_DataStream(t *testing.T) {
	lf := newTestLF(t)
	ctx := context.Background()

	wantContent := []byte("hello snapshot data")
	stream := makeTarGz(t, map[string][]byte{"state.json": wantContent})

	id, err := lf.Create(ctx, &types.SnapshotConfig{ID: testID(t), Name: "ds"}, stream)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, rc, err := lf.Restore(ctx, id)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	defer rc.Close()

	// Read the tar stream and find state.json.
	tr := tar.NewReader(rc)
	found := false
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Name == "state.json" {
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(tr); err != nil {
				t.Fatalf("read state.json from tar: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), wantContent) {
				t.Errorf("state.json content: got %q, want %q", buf.String(), string(wantContent))
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("state.json not found in restore stream")
	}
}
