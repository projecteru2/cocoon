package cloudimg

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/images"
	"github.com/cocoonstack/cocoon/progress"
	cloudimgProgress "github.com/cocoonstack/cocoon/progress/cloudimg"
	"github.com/cocoonstack/cocoon/storage"
	"github.com/cocoonstack/cocoon/utils"
)

// commit validates a pre-sniffed disk image source, converts it to
// qcow2 v3 if necessary, and atomically places the result as a blob at
// conf.BlobPath(digestHex) while updating the index under ref.
//
// IMPORTANT: commit does NOT sniff sourcePath for non-disk-image
// content. Callers MUST call sniffImageSource (or an equivalent check)
// first. This is a deliberate design choice so that callers holding an
// open download handle — notably the pull hot path via withDownload —
// can validate via ReadAt without reopening the file.
//
// canRename indicates whether commit may rename sourcePath into its
// intermediate blob-temp slot on the qcow2 v3 fast path:
//   - true:  sourcePath is cocoon-owned (download temp or import temp)
//     and may be moved.
//   - false: sourcePath is user-owned (direct file import) and must be
//     preserved; the fast path copies instead.
func commit(
	ctx context.Context,
	conf *Config,
	store storage.Store[imageIndex],
	ref string,
	tracker progress.Tracker,
	sourcePath string,
	digestHex string,
	canRename bool,
) error {
	logger := log.WithFunc("cloudimg.commit")

	blobPath := conf.BlobPath(digestHex)
	var tmpBlobPath string

	// Clean up the intermediate tmp blob on abort. Once the commit-phase
	// store.Update renames tmpBlobPath into blobPath, the path is gone
	// and os.Remove becomes a silent no-op.
	defer func() {
		if tmpBlobPath != "" {
			os.Remove(tmpBlobPath) //nolint:errcheck,gosec
		}
	}()

	if !utils.ValidFile(blobPath) {
		path, err := prepareTmpBlob(ctx, conf, tracker, sourcePath, digestHex, canRename)
		if err != nil {
			return err
		}
		tmpBlobPath = path
	}

	tracker.OnEvent(cloudimgProgress.Event{Phase: cloudimgProgress.PhaseCommit})

	if err := store.Update(ctx, func(idx *imageIndex) error {
		if tmpBlobPath != "" && !utils.ValidFile(blobPath) {
			if renameErr := os.Rename(tmpBlobPath, blobPath); renameErr != nil {
				return fmt.Errorf("rename blob: %w", renameErr)
			}
			if chmodErr := os.Chmod(blobPath, 0o444); chmodErr != nil { //nolint:gosec // G302: intentionally world-readable
				logger.Warnf(ctx, "chmod blob %s: %v", blobPath, chmodErr)
			}
		}

		info, statErr := os.Stat(blobPath)
		if statErr != nil {
			return fmt.Errorf("stat blob %s: %w", blobPath, statErr)
		}

		idx.Images[ref] = &imageEntry{
			Ref:        ref,
			ContentSum: images.NewDigest(digestHex),
			Size:       info.Size(),
			CreatedAt:  time.Now(),
		}
		return nil
	}); err != nil {
		return fmt.Errorf("update index: %w", err)
	}

	tracker.OnEvent(cloudimgProgress.Event{Phase: cloudimgProgress.PhaseDone})
	return nil
}

// prepareTmpBlob inspects sourcePath, then produces an intermediate
// qcow2 temp blob in conf.TempDir() ready for the commit-phase rename
// into blobPath. Returns the temp blob path on success.
//
// On the fast path (source is already qcow2 v3 with no backing file),
// canRename decides whether sourcePath is renamed or cloned. On the
// slow path, qemu-img convert writes into a digest-derived temp path.
func prepareTmpBlob(
	ctx context.Context,
	conf *Config,
	tracker progress.Tracker,
	sourcePath string,
	digestHex string,
	canRename bool,
) (string, error) {
	logger := log.WithFunc("cloudimg.commit")

	info, err := inspectImage(ctx, sourcePath)
	if err != nil {
		return "", fmt.Errorf("inspect image: %w", err)
	}
	logger.Debugf(ctx, "detected source format: %s (compat=%q, backing=%t)",
		info.Format, info.Compat, info.HasBackingFile)

	if info.Format == "qcow2" && info.Compat == "1.1" && !info.HasBackingFile {
		tmpBlobPath, err := fastPathTmpBlob(conf, sourcePath, digestHex, canRename)
		if err != nil {
			return "", err
		}
		logger.Debugf(ctx, "source already qcow2 v3, tmp blob at %s", tmpBlobPath)
		return tmpBlobPath, nil
	}

	// Slow path: qemu-img convert into a digest-derived temp path. Using
	// a well-known name (instead of CreateTemp) saves one open/close
	// pair and mirrors the fast path's naming.
	//
	// Concurrency: two parallel converters of identical content will race
	// on the same .tmp-<digest>.qcow2 path — qemu-img opens the target
	// O_CREAT|O_TRUNC, so interleaved writes mean "last writer wins". The
	// content is byte-identical so the surviving file is still valid, and
	// the commit-phase flock + ValidFile(blobPath) re-check serializes the
	// final rename into blobPath (the loser's rename no-ops). Duplicate
	// CPU work is wasted but correctness holds; see the follow-up note in
	// the refactor memo about flock-based slow-path de-duplication.
	tracker.OnEvent(cloudimgProgress.Event{Phase: cloudimgProgress.PhaseConvert})
	tmpBlobPath := conf.tmpBlobPath(digestHex)
	if err := convertToQcow2(ctx, info.Format, sourcePath, tmpBlobPath); err != nil {
		return "", err
	}
	logger.Debugf(ctx, "converted temp blob: %s", tmpBlobPath)
	return tmpBlobPath, nil
}

// fastPathTmpBlob produces a qcow2 temp blob from an already-qcow2-v3
// source. If canRename, the source is atomically renamed (same-filesystem
// assumption); otherwise it is copied so the caller's source file is
// preserved.
func fastPathTmpBlob(conf *Config, sourcePath, digestHex string, canRename bool) (string, error) {
	if canRename {
		// Digest-derived name shared with the slow path (see conf.tmpBlobPath).
		// Collisions with concurrent pulls/imports of identical content are
		// benign: content is identical and the commit-phase flock serializes
		// the final rename into blobPath.
		tmpBlobPath := conf.tmpBlobPath(digestHex)
		if err := os.Rename(sourcePath, tmpBlobPath); err != nil {
			return "", fmt.Errorf("rename tmp blob: %w", err)
		}
		return tmpBlobPath, nil
	}
	// Unique temp name to avoid cross-caller races: cloneToTemp cannot
	// share a single well-known path safely because concurrent writers
	// could interleave O_TRUNC with io.Copy.
	return cloneToTemp(sourcePath, conf.TempDir())
}

// cloneToTemp copies src into a fresh temp file in dir and returns the
// new path. Used by the !canRename fast path to preserve user files.
func cloneToTemp(src, dir string) (string, error) {
	srcFile, err := os.Open(src) //nolint:gosec // path is controlled
	if err != nil {
		return "", fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close() //nolint:errcheck

	dstFile, err := os.CreateTemp(dir, ".tmp-*.qcow2")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	dstPath := dstFile.Name()
	defer dstFile.Close() //nolint:errcheck

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		os.Remove(dstPath) //nolint:errcheck,gosec
		return "", fmt.Errorf("copy: %w", err)
	}
	if err := dstFile.Sync(); err != nil {
		os.Remove(dstPath) //nolint:errcheck,gosec
		return "", fmt.Errorf("sync: %w", err)
	}
	return dstPath, nil
}

// convertToQcow2 runs qemu-img convert to produce a qcow2 v3 (compat=1.1)
// blob at dst from src using the given source format. On failure, dst is
// removed and qemu-img stderr is included in the wrapped error.
func convertToQcow2(ctx context.Context, srcFormat, src, dst string) error {
	cmd := exec.CommandContext(ctx, "qemu-img", "convert", //nolint:gosec // args are controlled
		"-f", srcFormat, "-O", "qcow2", "-o", "compat=1.1",
		src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(dst) //nolint:errcheck,gosec
		return fmt.Errorf("qemu-img convert: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
