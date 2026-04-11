package cloudimg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/progress"
	cloudimgProgress "github.com/cocoonstack/cocoon/progress/cloudimg"
	"github.com/cocoonstack/cocoon/storage"
)

// importQcow2File imports a qcow2 file already on disk under name.
// The source file is user-owned and is not moved or modified — commit
// runs with canRename=false so the fast path copies rather than renames.
func importQcow2File(ctx context.Context, conf *Config, store storage.Store[imageIndex], name string, tracker progress.Tracker, filePath string) error {
	logger := log.WithFunc("cloudimg.import")

	tracker.OnEvent(cloudimgProgress.Event{Phase: cloudimgProgress.PhaseDownload})

	// Sniff the user-owned file before touching it. Opens a separate
	// handle because hashFile will reopen for the sha256 pass.
	f, err := os.Open(filePath) //nolint:gosec // filePath is caller input
	if err != nil {
		return fmt.Errorf("import %s: %w", filePath, err)
	}
	sniffErr := sniffImageSource(f)
	f.Close() //nolint:errcheck,gosec
	if sniffErr != nil {
		return fmt.Errorf("import %s: %w", filePath, sniffErr)
	}

	digestHex, err := hashFile(filePath)
	if err != nil {
		return fmt.Errorf("hash %s: %w", filePath, err)
	}
	logger.Debugf(ctx, "hashed %s -> sha256:%s", filePath, digestHex[:12])

	if err := commit(ctx, conf, store, name, tracker, filePath, digestHex, false); err != nil {
		return err
	}
	logger.Infof(ctx, "import complete: %s -> sha256:%s", name, digestHex)
	return nil
}

// importQcow2Reader buffers a stream to a cocoon-owned temp file, then
// imports it under name. canRename=true — the temp file is consumable.
//
// The stream's first 8 bytes are sniffed BEFORE buffering the rest so
// a bad upstream (HTML error page, gzip-wrapped image, etc.) is
// rejected without incurring a GB-scale write+hash pass.
func importQcow2Reader(ctx context.Context, conf *Config, store storage.Store[imageIndex], name string, tracker progress.Tracker, r io.Reader) error {
	logger := log.WithFunc("cloudimg.import")

	tracker.OnEvent(cloudimgProgress.Event{Phase: cloudimgProgress.PhaseDownload})

	// Sniff-first: peek the first 8 bytes off the reader and reject
	// obvious non-images before touching disk. The consumed bytes are
	// stitched back onto the reader via MultiReader so the subsequent
	// io.Copy sees the full stream.
	var head [8]byte
	n, err := io.ReadFull(r, head[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("import %s: read stream: %w", name, err)
	}
	if sniffErr := sniffHead(head[:n]); sniffErr != nil {
		return fmt.Errorf("import %s: %w", name, sniffErr)
	}
	full := io.MultiReader(bytes.NewReader(head[:n]), r)

	tmpFile, err := os.CreateTemp(conf.TempDir(), "import-*.img")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) //nolint:errcheck,gosec
	defer tmpFile.Close()    //nolint:errcheck,gosec

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, h), full); err != nil {
		return fmt.Errorf("copy to temp: %w", err)
	}

	digestHex := hex.EncodeToString(h.Sum(nil))
	logger.Debugf(ctx, "buffered stream -> sha256:%s", digestHex[:12])

	if err := commit(ctx, conf, store, name, tracker, tmpPath, digestHex, true); err != nil {
		return err
	}
	logger.Infof(ctx, "import complete: %s -> sha256:%s", name, digestHex)
	return nil
}

// importQcow2Concat concatenates multiple source files into a cocoon-owned
// temp file and imports the result under name. canRename=true.
func importQcow2Concat(ctx context.Context, conf *Config, store storage.Store[imageIndex], name string, tracker progress.Tracker, file ...string) error {
	logger := log.WithFunc("cloudimg.import")

	if len(file) == 0 {
		return errors.New("no qcow2 files provided")
	}
	for _, f := range file {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("file %s: %w", f, err)
		}
	}

	tracker.OnEvent(cloudimgProgress.Event{Phase: cloudimgProgress.PhaseDownload})

	tmpFile, err := os.CreateTemp(conf.TempDir(), "import-*.img")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) //nolint:errcheck,gosec
	defer tmpFile.Close()    //nolint:errcheck,gosec

	h := sha256.New()
	w := io.MultiWriter(tmpFile, h)

	for _, filePath := range file {
		src, openErr := os.Open(filePath) //nolint:gosec
		if openErr != nil {
			return fmt.Errorf("open %s: %w", filePath, openErr)
		}
		_, copyErr := io.Copy(w, src)
		src.Close() //nolint:errcheck,gosec
		if copyErr != nil {
			return fmt.Errorf("copy %s: %w", filePath, copyErr)
		}
	}

	digestHex := hex.EncodeToString(h.Sum(nil))
	logger.Debugf(ctx, "concatenated %d file(s) -> sha256:%s", len(file), digestHex[:12])

	if err := sniffImageSource(tmpFile); err != nil {
		return fmt.Errorf("import %s: %w", name, err)
	}

	if err := commit(ctx, conf, store, name, tracker, tmpPath, digestHex, true); err != nil {
		return err
	}
	logger.Infof(ctx, "import complete: %s -> sha256:%s", name, digestHex)
	return nil
}

// hashFile computes the sha256 digest of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
