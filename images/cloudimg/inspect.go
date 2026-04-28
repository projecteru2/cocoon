package cloudimg

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

var (
	// qcow2Magic is the qcow2 file signature.
	qcow2Magic = []byte{'Q', 'F', 'I', 0xfb}

	// nonImageSignatures catches common payloads qemu-img would misclassify as raw.
	nonImageSignatures = []struct {
		prefix []byte
		desc   string
	}{
		{[]byte("<!"), "content looks like HTML/XML, not a disk image"},
		{[]byte("<?"), "content looks like HTML/XML, not a disk image"},
		{[]byte("<h"), "content looks like HTML, not a disk image"},
		{[]byte("<H"), "content looks like HTML, not a disk image"},
		{[]byte{0x1f, 0x8b}, "content is gzip-compressed (cloudimg does not auto-decompress)"},
		{[]byte("\xfd7zXZ\x00"), "content is xz-compressed (cloudimg does not auto-decompress)"},

		{[]byte("BZh"), "content is bzip2-compressed (cloudimg does not auto-decompress)"},
		{[]byte{0x28, 0xb5, 0x2f, 0xfd}, "content is zstd-compressed (cloudimg does not auto-decompress)"},
		{[]byte("PK"), "content is a zip archive, not a disk image"},
		{[]byte{0x37, 0x7a, 0xbc, 0xaf, 0x27, 0x1c}, "content is a 7z archive, not a disk image"},
	}
)

type sourceImageInfo struct {
	Format         string // "qcow2" or "raw"
	Compat         string // qcow2 compat level (e.g. "0.10", "1.1"); empty for non-qcow2
	HasBackingFile bool
}

func sniffImageSource(f *os.File) error {
	head, err := peekHead(f, 8)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	return sniffHead(head)
}

func sniffHead(head []byte) error {
	if len(head) < 4 {
		return fmt.Errorf("content too small to be a disk image (%d bytes)", len(head))
	}
	if bytes.HasPrefix(head, qcow2Magic) {
		return nil
	}
	for _, sig := range nonImageSignatures {
		if bytes.HasPrefix(head, sig.prefix) {
			return errors.New(sig.desc)
		}
	}
	return nil
}

func peekHead(f *os.File, n int) ([]byte, error) {
	buf := make([]byte, n)
	m, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:m], nil
}

func inspectImage(ctx context.Context, path string) (*sourceImageInfo, error) {
	info, isQcow2, err := inspectQcow2Header(path)
	if err != nil {
		return nil, err
	}
	if isQcow2 {
		return info, nil
	}
	// Not qcow2: delegate to qemu-img to distinguish raw from unsupported
	// formats (vmdk/vdi/vhd/etc.). Blindly treating non-qcow2 as raw would
	// let convertToQcow2 silently produce corrupt output.
	// shell out because no Go inspector covers the full disk format matrix; qemu-img is authoritative.
	cmd := exec.CommandContext(ctx, "qemu-img", "info", "--output=json", path) //nolint:gosec // path is controlled
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("qemu-img info %s: %w", path, err)
	}
	var raw struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse qemu-img info: %w", err)
	}
	if raw.Format != "raw" {
		return nil, fmt.Errorf("unsupported source format %q (expected qcow2 or raw)", raw.Format)
	}
	return &sourceImageInfo{Format: "raw"}, nil
}

// inspectQcow2Header parses the qcow2 header fields needed by prepareTmpBlob
// without forking qemu-img. Returns (info, true, nil) on valid qcow2,
// (nil, false, nil) if the file is not qcow2, or an error if the header is
// truncated or the version is unsupported.
func inspectQcow2Header(path string) (*sourceImageInfo, bool, error) {
	f, err := os.Open(path) //nolint:gosec // path is caller-controlled
	if err != nil {
		return nil, false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck

	// qcow2 header: magic(4) + version(4) + backing_file_offset(8) = 16 bytes.
	var header [16]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return nil, false, nil
	}
	if !bytes.Equal(header[:4], qcow2Magic) {
		return nil, false, nil
	}
	var compat string
	switch version := binary.BigEndian.Uint32(header[4:8]); version {
	case 2:
		compat = "0.10"
	case 3:
		compat = "1.1"
	default:
		return nil, true, fmt.Errorf("unsupported qcow2 version %d", version)
	}
	return &sourceImageInfo{
		Format:         "qcow2",
		Compat:         compat,
		HasBackingFile: binary.BigEndian.Uint64(header[8:16]) != 0,
	}, true, nil
}
