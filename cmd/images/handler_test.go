package images

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

// qcow2Magic is the 4-byte magic header for qcow2 files.
var qcow2Magic = []byte{'Q', 'F', 'I', 0xfb}

func gzipWrap(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDetectReader_RawQcow2(t *testing.T) {
	// QFI magic followed by some data.
	data := append(qcow2Magic, make([]byte, 100)...)
	r, typ, cleanup, err := detectReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("detectReader: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if typ != imageTypeQcow2 {
		t.Errorf("type: got %d, want imageTypeQcow2", typ)
	}

	// Reader should still be consumable (peek doesn't consume).
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch: got %d bytes, want %d", len(got), len(data))
	}
}

func TestDetectReader_GzipQcow2(t *testing.T) {
	data := append(qcow2Magic, make([]byte, 100)...)
	gz := gzipWrap(t, data)

	r, typ, cleanup, err := detectReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("detectReader: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if typ != imageTypeQcow2 {
		t.Errorf("type: got %d, want imageTypeQcow2", typ)
	}

	// Reader should return the uncompressed qcow2 data.
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch: got %d bytes, want %d", len(got), len(data))
	}
}

func TestDetectReader_RawTar(t *testing.T) {
	// Anything that doesn't start with QFI or gzip magic.
	data := []byte("this is a tar-like stream of data, not really tar but not qcow2 either")
	r, typ, cleanup, err := detectReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("detectReader: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if typ != imageTypeTar {
		t.Errorf("type: got %d, want imageTypeTar", typ)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch")
	}
}

func TestDetectReader_GzipTar(t *testing.T) {
	data := []byte("tar content without qcow2 magic prefix padding here for length")
	gz := gzipWrap(t, data)

	r, typ, cleanup, err := detectReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("detectReader: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if typ != imageTypeTar {
		t.Errorf("type: got %d, want imageTypeTar", typ)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch: got %q, want %q", got, data)
	}
}

func TestDetectReader_TooShort(t *testing.T) {
	_, _, _, err := detectReader(bytes.NewReader([]byte{0x00}))
	if err == nil {
		t.Fatal("expected error for 1-byte input")
	}
}

func TestDetectReader_Empty(t *testing.T) {
	_, _, _, err := detectReader(bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestDetectReader_GzipPreservesFullContent(t *testing.T) {
	// Verify that after gzip detection + unwrap, the full content is readable
	// and matches the original (no bytes lost to peeking).
	original := make([]byte, 16384) // larger than bufio default
	for i := range original {
		original[i] = byte(i % 251) // non-trivial pattern
	}

	gz := gzipWrap(t, original)
	r, typ, cleanup, err := detectReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("detectReader: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if typ != imageTypeTar {
		t.Errorf("type: got %d, want imageTypeTar (non-qcow2 content)", typ)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("content mismatch: got %d bytes, want %d", len(got), len(original))
	}
}
