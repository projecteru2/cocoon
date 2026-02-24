package cloudimg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/progress"
	cloudimgProgress "github.com/projecteru2/cocoon/progress/cloudimg"
	"github.com/projecteru2/cocoon/storage"
	"github.com/projecteru2/cocoon/utils"
	"github.com/projecteru2/core/log"
)

const (
	// urlDownloadTimeout is the overall timeout for cloud image URL downloads.
	urlDownloadTimeout = 30 * time.Minute

	// maxDownloadBytes is the maximum allowed download size (20 GiB).
	maxDownloadBytes int64 = 20 << 30

	// report every 1 MiB
	progressInterval = 1 << 20
)

func pull(ctx context.Context, conf *config.Config, store storage.Store[imageIndex], url string, tracker progress.Tracker) error {
	logger := log.WithFunc("cloudimg.pull")

	// Idempotency check: if the URL is already indexed and the blob is valid, skip.
	var skip bool
	if err := store.With(ctx, func(idx *imageIndex) error {
		if _, entry, ok := idx.Lookup(url); ok {
			blobPath := conf.CloudimgBlobPath(entry.ContentSum.Hex())
			if utils.ValidFile(blobPath) {
				logger.Infof(ctx, "image %s already cached, skipping", url)
				skip = true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if skip {
		return nil
	}

	// Download and convert (blob not placed yet — returned as temp path).
	digestHex, tmpBlobPath, err := downloadAndConvert(ctx, conf, url, tracker)
	if err != nil {
		return err
	}
	// Clean up temp blob on exit if it wasn't consumed by rename.
	if tmpBlobPath != "" {
		defer os.Remove(tmpBlobPath) //nolint:errcheck
	}

	// Commit: place blob + update index atomically under flock.
	// Both operations happen inside store.Update so GC cannot see
	// an unreferenced blob between placement and index write.
	tracker.OnEvent(cloudimgProgress.Event{Phase: cloudimgProgress.PhaseCommit})

	if err := store.Update(ctx, func(idx *imageIndex) error {
		blobPath := conf.CloudimgBlobPath(digestHex)

		// Place blob if not already present (content dedup or concurrent pull).
		if tmpBlobPath != "" && !utils.ValidFile(blobPath) {
			if err := os.Rename(tmpBlobPath, blobPath); err != nil {
				return fmt.Errorf("rename blob: %w", err)
			}
			if err := os.Chmod(blobPath, 0o444); err != nil { //nolint:gosec // G302: intentionally world-readable
				return fmt.Errorf("chmod blob: %w", err)
			}
		}

		info, err := os.Stat(blobPath)
		if err != nil {
			return fmt.Errorf("stat blob %s: %w", blobPath, err)
		}

		idx.Images[url] = &imageEntry{
			Ref:        url,
			ContentSum: images.NewDigest(digestHex),
			Size:       info.Size(),
			CreatedAt:  time.Now(),
		}
		return nil
	}); err != nil {
		return fmt.Errorf("update index: %w", err)
	}

	tracker.OnEvent(cloudimgProgress.Event{Phase: cloudimgProgress.PhaseDone})
	logger.Infof(ctx, "pull complete: %s -> sha256:%s", url, digestHex)
	return nil
}

// downloadAndConvert downloads the image from URL and converts to qcow2.
// Returns (digestHex, tmpBlobPath, err). tmpBlobPath is empty when the blob
// already exists on disk; otherwise the caller is responsible for placing
// (renaming) and cleaning up the temp file.
func downloadAndConvert(ctx context.Context, conf *config.Config, url string, tracker progress.Tracker) (string, string, error) {
	logger := log.WithFunc("cloudimg.downloadAndConvert")

	// Create temp file for download.
	tmpFile, err := os.CreateTemp(conf.CloudimgTempDir(), "pull-*.img")
	if err != nil {
		return "", "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) //nolint:errcheck

	// Download.
	digestHex, err := download(ctx, url, tmpFile, tracker)
	if err != nil {
		return "", "", err
	}
	logger.Infof(ctx, "downloaded %s -> %s (sha256:%s)", url, tmpPath, digestHex)

	// Check if blob already exists (another URL might have same content).
	blobPath := conf.CloudimgBlobPath(digestHex)
	if utils.ValidFile(blobPath) {
		logger.Infof(ctx, "blob %s already exists, skipping conversion", digestHex)
		return digestHex, "", nil
	}

	// Detect format and convert.
	tracker.OnEvent(cloudimgProgress.Event{Phase: cloudimgProgress.PhaseConvert})

	format, err := detectImageFormat(ctx, tmpPath)
	if err != nil {
		return "", "", fmt.Errorf("detect format: %w", err)
	}
	logger.Infof(ctx, "detected source format: %s", format)

	// Convert to qcow2 v3 (compat=1.1).
	// Create temp in the temp dir (not blobs dir) so GC won't delete it
	// while qemu-img is still writing.
	tmpBlob, err := os.CreateTemp(conf.CloudimgTempDir(), ".tmp-*.qcow2")
	if err != nil {
		return "", "", fmt.Errorf("create temp blob: %w", err)
	}
	tmpBlobPath := tmpBlob.Name()
	tmpBlob.Close() //nolint:errcheck

	cmd := exec.CommandContext(ctx, "qemu-img", "convert",
		"-f", format, "-O", "qcow2", "-o", "compat=1.1",
		tmpPath, tmpBlobPath) //nolint:gosec // args are controlled internal paths
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmpBlobPath) //nolint:errcheck
		return "", "", fmt.Errorf("qemu-img convert: %s: %w", strings.TrimSpace(string(out)), err)
	}

	logger.Infof(ctx, "converted temp blob: %s", tmpBlobPath)
	return digestHex, tmpBlobPath, nil
}

// download fetches the URL content into dst, computing SHA-256 along the way.
func download(ctx context.Context, url string, dst *os.File, tracker progress.Tracker) (string, error) {
	defer dst.Close() //nolint:errcheck

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create HTTP request: %w", err)
	}

	client := &http.Client{Timeout: urlDownloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP GET %s: status %d %s", url, resp.StatusCode, resp.Status)
	}

	contentLength := resp.ContentLength
	tracker.OnEvent(cloudimgProgress.Event{
		Phase:      cloudimgProgress.PhaseDownload,
		BytesTotal: contentLength,
	})

	h := sha256.New()
	limitedBody := io.LimitReader(resp.Body, maxDownloadBytes+1)
	reader := io.TeeReader(limitedBody, h)

	pw := &progressWriter{w: dst, total: contentLength, tracker: tracker}
	written, err := io.Copy(pw, reader)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if written > maxDownloadBytes {
		return "", fmt.Errorf("download %s: exceeded max size (%d bytes)", url, maxDownloadBytes)
	}

	if err := dst.Sync(); err != nil {
		return "", fmt.Errorf("sync temp file: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// detectImageFormat uses qemu-img info to determine the disk image format.
func detectImageFormat(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "qemu-img", "info", "--output=json", path) //nolint:gosec // path is controlled
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("qemu-img info %s: %w", path, err)
	}
	// Parse only the top-level "format" field. The JSON output contains
	// nested "children" objects with "format": "file" (protocol layer)
	// which must not be confused with the actual disk image format.
	var info struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return "", fmt.Errorf("parse qemu-img info: %w", err)
	}
	if info.Format != "qcow2" && info.Format != "raw" {
		return "", fmt.Errorf("unsupported source format %q (expected qcow2 or raw)", info.Format)
	}
	return info.Format, nil
}

// progressWriter wraps an io.Writer and periodically emits download progress events.
type progressWriter struct {
	w          io.Writer
	written    int64
	total      int64
	tracker    progress.Tracker
	lastReport int64
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	pw.written += int64(n)
	if pw.written-pw.lastReport >= progressInterval {
		pw.lastReport = pw.written
		pw.tracker.OnEvent(cloudimgProgress.Event{
			Phase:      cloudimgProgress.PhaseDownload,
			BytesTotal: pw.total,
			BytesDone:  pw.written,
		})
	}
	return n, err
}
