package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTarFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.txt")
	content := []byte("hello tar world")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := TarFile(tw, src, "custom-name.txt"); err != nil {
		t.Fatalf("TarFile: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	// Read back.
	tr := tar.NewReader(&buf)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar.Next: %v", err)
	}
	if hdr.Name != "custom-name.txt" {
		t.Errorf("name: got %q, want %q", hdr.Name, "custom-name.txt")
	}
	if hdr.Size != int64(len(content)) {
		t.Errorf("size: got %d, want %d", hdr.Size, len(content))
	}
	got, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}

	// No more entries.
	if _, err := tr.Next(); err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestTarFile_NotExist(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := TarFile(tw, "/nonexistent/file.txt", "file.txt"); err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestTarDir(t *testing.T) {
	dir := t.TempDir()

	// Create a few files.
	files := map[string][]byte{
		"a.txt":    []byte("aaa"),
		"b.bin":    []byte("bbb"),
		"c.config": []byte("ccc"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a subdirectory — should be skipped (not regular).
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := TarDir(tw, dir); err != nil {
		t.Fatalf("TarDir: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	// Read back and verify all files are present.
	tr := tar.NewReader(&buf)
	found := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		found[hdr.Name] = data
	}

	if len(found) != len(files) {
		t.Errorf("entry count: got %d, want %d", len(found), len(files))
	}
	for name, want := range files {
		got, ok := found[name]
		if !ok {
			t.Errorf("missing entry %q", name)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestTarDir_Empty(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := TarDir(tw, dir); err != nil {
		t.Fatalf("TarDir: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	tr := tar.NewReader(&buf)
	if _, err := tr.Next(); err != io.EOF {
		t.Errorf("expected EOF for empty dir, got %v", err)
	}
}

func TestTarDir_NotExist(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := TarDir(tw, "/nonexistent/dir"); err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}

// helper: create a tar.gz in memory from a map of name→content.
func makeTarGz(t *testing.T, files map[string][]byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
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
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractTarGz(t *testing.T) {
	files := map[string][]byte{
		"a.txt": []byte("hello"),
		"b.bin": []byte("world"),
	}
	buf := makeTarGz(t, files)

	dir := t.TempDir()
	if err := ExtractTarGz(dir, buf); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}

	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestExtractTarGz_SkipsDirectories(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Add a directory entry.
	tw.WriteHeader(&tar.Header{Name: "subdir/", Typeflag: tar.TypeDir, Mode: 0o755})
	// Add a regular file.
	tw.WriteHeader(&tar.Header{Name: "file.txt", Size: 3, Typeflag: tar.TypeReg, Mode: 0o644})
	tw.Write([]byte("abc"))
	tw.Close()
	gw.Close()

	dir := t.TempDir()
	if err := ExtractTarGz(dir, &buf); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}

	// Only file.txt should exist, no subdir.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "file.txt" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected only file.txt, got %v", names)
	}
}

func TestExtractTarGz_PathTraversal(t *testing.T) {
	files := map[string][]byte{
		"../../../etc/passwd": []byte("evil"),
		"normal.txt":          []byte("safe"),
	}
	buf := makeTarGz(t, files)

	dir := t.TempDir()
	if err := ExtractTarGz(dir, buf); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}

	// The traversal path should be sanitized to just "passwd" (base name).
	got, err := os.ReadFile(filepath.Join(dir, "passwd"))
	if err != nil {
		t.Fatalf("expected passwd to be extracted (sanitized): %v", err)
	}
	if !bytes.Equal(got, []byte("evil")) {
		t.Errorf("passwd content: got %q", got)
	}

	// Verify nothing was written outside the dir.
	if _, err := os.Stat(filepath.Join(dir, "..", "etc")); err == nil {
		t.Error("path traversal was not prevented")
	}
}

func TestExtractTarGz_Empty(t *testing.T) {
	buf := makeTarGz(t, nil)

	dir := t.TempDir()
	if err := ExtractTarGz(dir, buf); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected empty dir, got %d entries", len(entries))
	}
}

func TestExtractTarGz_InvalidGzip(t *testing.T) {
	if err := ExtractTarGz(t.TempDir(), strings.NewReader("not gzip")); err == nil {
		t.Fatal("expected error for invalid gzip")
	}
}

func TestExtractTarGz_RoundTrip(t *testing.T) {
	// TarDir → gzip → ExtractTarGz round trip.
	srcDir := t.TempDir()
	files := map[string][]byte{
		"config.json":  []byte(`{"key":"value"}`),
		"state.json":   []byte(`{"state":"paused"}`),
		"memory-ranges": bytes.Repeat([]byte{0xAB}, 1024),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(srcDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Pack.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := TarDir(tw, srcDir); err != nil {
		t.Fatalf("TarDir: %v", err)
	}
	tw.Close()
	gw.Close()

	// Extract.
	dstDir := t.TempDir()
	if err := ExtractTarGz(dstDir, &buf); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}

	// Verify.
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dstDir, name))
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s content mismatch", name)
		}
	}
}
