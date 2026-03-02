package utils

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// PAX record keys for our custom sparse format.
// The tar entry Size equals the sum of data segment lengths (not the logical
// file size), so only actual data is stored and compressed.
const (
	paxSparseMap  = "COCOON.sparse.map"
	paxSparseSize = "COCOON.sparse.size"
)

// sparseSegment describes one contiguous data region in a sparse file.
type sparseSegment struct {
	Offset int64 `json:"o"`
	Length int64 `json:"l"`
}

// TarDir writes all regular files in dir into tw as flat tar entries (no directory nesting).
// On Linux, sparse files are detected and only their data segments are stored.
func TarDir(tw *tar.Writer, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if err := tarFileMaybeSparse(tw, filepath.Join(dir, entry.Name()), entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// TarFile writes a single file into tw with the given name.
func TarFile(tw *tar.Writer, path, nameInTar string) error {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return fmt.Errorf("tar header for %s: %w", path, err)
	}
	hdr.Name = nameInTar

	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write header %s: %w", nameInTar, err)
	}

	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("write data %s: %w", nameInTar, err)
	}
	return nil
}

// ExtractTarGz decompresses a gzip stream, then extracts tar entries as flat
// files into dir. Only regular files are extracted; the base name is used to
// prevent path traversal.
//
// Entries with COCOON.sparse PAX records are extracted using the embedded
// sparse map, writing data segments to their original offsets and leaving
// holes untouched. This preserves sparsity without scanning for zero blocks.
func ExtractTarGz(dir string, r io.Reader) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close() //nolint:errcheck

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		name := filepath.Base(hdr.Name)
		if name == "." || name == ".." {
			continue
		}

		outPath := filepath.Join(dir, name)

		if mapJSON, ok := hdr.PAXRecords[paxSparseMap]; ok {
			realSize, parseErr := strconv.ParseInt(hdr.PAXRecords[paxSparseSize], 10, 64)
			if parseErr != nil {
				return fmt.Errorf("parse sparse size for %s: %w", name, parseErr)
			}
			if err := extractFileSparse(outPath, tr, hdr.FileInfo().Mode(), realSize, mapJSON); err != nil {
				return fmt.Errorf("extract sparse %s: %w", name, err)
			}
		} else {
			if err := extractFile(outPath, tr, hdr.FileInfo().Mode()); err != nil {
				return fmt.Errorf("extract %s: %w", name, err)
			}
		}
	}
}

// extractFileSparse creates a sparse file at path.
// It truncates the file to realSize (all holes), then writes each data
// segment from r to the correct offset.
func extractFileSparse(path string, r io.Reader, perm os.FileMode, realSize int64, mapJSON string) error {
	var segments []sparseSegment
	if err := json.Unmarshal([]byte(mapJSON), &segments); err != nil {
		return fmt.Errorf("decode sparse map: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck

	// Set full logical size — the entire file starts as a hole.
	if err := f.Truncate(realSize); err != nil {
		return err
	}

	// Write each data segment to its correct offset.
	// The tar entry contains concatenated segment data in order.
	for _, seg := range segments {
		if _, err := f.Seek(seg.Offset, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.CopyN(f, r, seg.Length); err != nil {
			return err
		}
	}

	return f.Sync()
}

// sparseBlockSize is the block size used for zero-detection during extraction.
// Matching the typical filesystem block size ensures seeks create actual holes.
const sparseBlockSize = 4096

// extractFile creates a file at path and copies content from r.
// Zero-filled blocks are written as holes (seek instead of write) to preserve sparsity.
// Used as fallback when COCOON.sparse PAX records are not present.
func extractFile(path string, r io.Reader, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck

	buf := make([]byte, sparseBlockSize)
	var total int64
	endsWithHole := false

	for {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			var writeErr error
			endsWithHole, writeErr = writeBlockSparse(f, buf[:n])
			if writeErr != nil {
				return writeErr
			}
			total += int64(n)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	// If the file ends with a hole, the OS file size won't reflect the seek.
	// Truncate sets the correct logical size.
	if endsWithHole {
		if err := f.Truncate(total); err != nil {
			return err
		}
	}

	return f.Sync()
}

// writeBlockSparse writes chunk to f. If chunk is all zeros, it seeks forward
// (creating a hole) instead. Returns whether the block was a hole.
func writeBlockSparse(f *os.File, chunk []byte) (hole bool, err error) {
	if isAllZero(chunk) {
		_, err = f.Seek(int64(len(chunk)), io.SeekCurrent)
		return true, err
	}
	_, err = f.Write(chunk)
	return false, err
}

// isAllZero reports whether every byte in b is zero.
func isAllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
