package oci

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/sync/errgroup"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
	"github.com/projecteru2/core/log"
)

// pullLayerResult holds the output of processing a single layer.
type pullLayerResult struct {
	index      int
	digest     types.Digest
	erofsPath  string
	kernelPath string // non-empty if this layer contains a kernel
	initrdPath string // non-empty if this layer contains an initrd
}

// pull downloads an OCI image, extracts boot files, and converts each layer
// to EROFS concurrently using errgroup.
func pull(ctx context.Context, cfg *config.Config, idx *imageIndex, imageRef string) error {
	logger := log.WithFunc("oci.pull")

	ref, digestHex, results, err := fetchAndProcess(ctx, cfg, idx, imageRef)
	if err != nil {
		return err
	}
	if results == nil {
		logger.Infof(ctx, "Already up to date: %s (digest: sha256:%s)", ref, digestHex)
		return nil
	}

	// Commit artifacts and update index atomically under flock.
	manifestDigest := types.NewDigest(digestHex)
	if err := idx.Update(ctx, func(idx *imageIndex) error {
		// Double-check: another process may have completed while we were working.
		if existing, ok := idx.Images[ref]; ok && existing.ManifestDigest == manifestDigest {
			return nil
		}
		return commitAndRecord(cfg, idx, ref, manifestDigest, results)
	}); err != nil {
		return fmt.Errorf("update image index: %w", err)
	}

	logger.Infof(ctx, "Pulled: %s (digest: sha256:%s, layers: %d)", ref, digestHex, len(results))
	return nil
}

// fetchAndProcess downloads the image and processes all layers concurrently.
// Returns nil results if the image is already up-to-date.
func fetchAndProcess(ctx context.Context, cfg *config.Config, idx *imageIndex, imageRef string) (string, string, []pullLayerResult, error) {
	logger := log.WithFunc("oci.pull")

	parsedRef, err := name.ParseReference(imageRef)
	if err != nil {
		return "", "", nil, fmt.Errorf("invalid image reference %q: %w", imageRef, err)
	}
	ref := parsedRef.String()

	platform := v1.Platform{
		Architecture: runtime.GOARCH,
		OS:           "linux",
	}

	logger.Infof(ctx, "Pulling image: %s", ref)

	img, err := remote.Image(parsedRef,
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithContext(ctx),
		remote.WithPlatform(platform),
	)
	if err != nil {
		return "", "", nil, fmt.Errorf("fetch image %s: %w", ref, err)
	}

	manifestDigest, err := img.Digest()
	if err != nil {
		return "", "", nil, fmt.Errorf("get manifest digest: %w", err)
	}
	digestHex := manifestDigest.Hex

	// Idempotency: check if already pulled with same manifest.
	var alreadyPulled bool
	if err := idx.With(ctx, func(idx *imageIndex) error {
		if entry, ok := idx.Images[ref]; ok && entry.ManifestDigest == types.NewDigest(digestHex) {
			alreadyPulled = true
		}
		return nil
	}); err != nil {
		return "", "", nil, fmt.Errorf("read image index: %w", err)
	}
	if alreadyPulled {
		return ref, digestHex, nil, nil
	}

	layers, err := img.Layers()
	if err != nil {
		return "", "", nil, fmt.Errorf("get layers: %w", err)
	}
	if len(layers) == 0 {
		return "", "", nil, fmt.Errorf("image %s has no layers", ref)
	}

	// Create working directory under temp.
	workDir, err := os.MkdirTemp(cfg.TempDir(), "pull-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir) //nolint:errcheck

	// Process layers concurrently with bounded parallelism.
	results := make([]pullLayerResult, len(layers))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.PoolSize)

	for i, layer := range layers {
		layerIdx := i
		layerRef := layer
		g.Go(func() error {
			return processLayer(gctx, cfg, layerIdx, layerRef, workDir, &results[layerIdx])
		})
	}

	if err := g.Wait(); err != nil {
		return "", "", nil, fmt.Errorf("process layers: %w", err)
	}

	return ref, digestHex, results, nil
}

// commitAndRecord moves artifacts to shared storage and records the image entry.
// Must be called under flock (inside idx.Update).
func commitAndRecord(cfg *config.Config, idx *imageIndex, ref string, manifestDigest types.Digest, results []pullLayerResult) error {
	var (
		layerEntries []layerEntry
		kernelLayer  types.Digest
		initrdLayer  types.Digest
	)

	for i := range results {
		r := &results[i]
		layerDigestHex := r.digest.Hex()

		// Move erofs to shared blob path if not already there.
		if r.erofsPath != cfg.BlobPath(layerDigestHex) {
			if err := os.Rename(r.erofsPath, cfg.BlobPath(layerDigestHex)); err != nil {
				return fmt.Errorf("move layer %d erofs: %w", r.index, err)
			}
		}

		// Move boot files to shared boot dir if not already there.
		if r.kernelPath != "" && r.kernelPath != cfg.KernelPath(layerDigestHex) {
			if err := os.MkdirAll(cfg.BootDir(layerDigestHex), 0o750); err != nil {
				return fmt.Errorf("create boot dir for layer %d: %w", r.index, err)
			}
			if err := os.Rename(r.kernelPath, cfg.KernelPath(layerDigestHex)); err != nil {
				return fmt.Errorf("move layer %d kernel: %w", r.index, err)
			}
		}
		if r.initrdPath != "" && r.initrdPath != cfg.InitrdPath(layerDigestHex) {
			if err := os.MkdirAll(cfg.BootDir(layerDigestHex), 0o750); err != nil {
				return fmt.Errorf("create boot dir for layer %d: %w", r.index, err)
			}
			if err := os.Rename(r.initrdPath, cfg.InitrdPath(layerDigestHex)); err != nil {
				return fmt.Errorf("move layer %d initrd: %w", r.index, err)
			}
		}

		// Track which layer provides boot files (later layers win per OCI ordering).
		if r.kernelPath != "" {
			kernelLayer = r.digest
		}
		if r.initrdPath != "" {
			initrdLayer = r.digest
		}

		layerEntries = append(layerEntries, layerEntry{Digest: r.digest})
	}

	if kernelLayer == "" || initrdLayer == "" {
		return fmt.Errorf("image %s missing boot files (vmlinuz/initrd.img)", ref)
	}

	idx.Images[ref] = &imageEntry{
		Ref:            ref,
		ManifestDigest: manifestDigest,
		Layers:         layerEntries,
		KernelLayer:    kernelLayer,
		InitrdLayer:    initrdLayer,
		CreatedAt:      time.Now().UTC(),
	}
	return nil
}

// processLayer handles a single layer: extracts boot files and converts to EROFS
// in a single pass using io.TeeReader. If the layer is already cached, it checks
// for missing boot files and self-heals by re-extracting them.
func processLayer(ctx context.Context, cfg *config.Config, idx int, layer v1.Layer, workDir string, result *pullLayerResult) error {
	logger := log.WithFunc("oci.processLayer")

	layerDigest, err := layer.Digest()
	if err != nil {
		return fmt.Errorf("get digest: %w", err)
	}
	digestHex := layerDigest.Hex

	result.index = idx
	result.digest = types.NewDigest(digestHex)

	// Check if this layer's blob already exists (shared across images).
	if _, err := os.Stat(cfg.BlobPath(digestHex)); err == nil {
		logger.Infof(ctx, "Layer %d: sha256:%s already cached", idx, digestHex[:12])
		result.erofsPath = cfg.BlobPath(digestHex)

		// Check for cached boot files.
		result.kernelPath = existsOrEmpty(cfg.KernelPath(digestHex))
		result.initrdPath = existsOrEmpty(cfg.InitrdPath(digestHex))

		// Self-heal: boot dir exists (layer had boot files) but some are missing.
		if _, statErr := os.Stat(cfg.BootDir(digestHex)); statErr == nil {
			if result.kernelPath == "" || result.initrdPath == "" {
				logger.Infof(ctx, "Layer %d: sha256:%s self-healing missing boot files", idx, digestHex[:12])
				rc, rcErr := layer.Uncompressed()
				if rcErr != nil {
					return fmt.Errorf("open layer for boot extraction: %w", rcErr)
				}
				defer rc.Close() //nolint:errcheck

				kp, ip, scanErr := scanBootFiles(ctx, rc, workDir, digestHex)
				if scanErr != nil {
					return fmt.Errorf("self-heal boot files: %w", scanErr)
				}
				if result.kernelPath == "" {
					result.kernelPath = kp
				}
				if result.initrdPath == "" {
					result.initrdPath = ip
				}
			}
		}
		return nil
	}

	logger.Infof(ctx, "Layer %d: sha256:%s -> erofs (single-pass)", idx, digestHex[:12])

	// Open uncompressed tar stream once.
	rc, err := layer.Uncompressed()
	if err != nil {
		return fmt.Errorf("open uncompressed layer: %w", err)
	}
	defer rc.Close() //nolint:errcheck

	erofsPath := filepath.Join(workDir, digestHex+".erofs")
	layerUUID := utils.UUIDv5(digestHex)

	// Start mkfs.erofs in background, receiving the tar stream via pipe.
	cmd, erofsStdin, output, err := startErofsConversion(ctx, layerUUID, erofsPath)
	if err != nil {
		return fmt.Errorf("start erofs conversion: %w", err)
	}

	// TeeReader: every byte read for boot scanning also feeds mkfs.erofs.
	tee := io.TeeReader(rc, erofsStdin)
	kernelPath, initrdPath, scanErr := scanBootFiles(ctx, tee, workDir, digestHex)

	// Drain remaining tar data to ensure mkfs.erofs receives the complete stream.
	if scanErr == nil {
		if _, drainErr := io.Copy(io.Discard, tee); drainErr != nil {
			scanErr = fmt.Errorf("drain layer stream: %w", drainErr)
		}
	}
	_ = erofsStdin.Close()

	if waitErr := cmd.Wait(); waitErr != nil {
		return fmt.Errorf("mkfs.erofs failed: %w (output: %s)", waitErr, output.String())
	}
	if scanErr != nil {
		return fmt.Errorf("scan boot files: %w", scanErr)
	}

	result.kernelPath = kernelPath
	result.initrdPath = initrdPath
	result.erofsPath = erofsPath
	return nil
}

// existsOrEmpty returns path if the file exists, empty string otherwise.
func existsOrEmpty(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// scanBootFiles reads a tar stream and extracts kernel/initrd files.
// Accepts both tar.TypeReg and deprecated tar.TypeRegA. Excludes .old variants.
// Files are written to workDir with digest-based names.
func scanBootFiles(ctx context.Context, r io.Reader, workDir, digestHex string) (kernelPath, initrdPath string, err error) {
	logger := log.WithFunc("oci.scanBootFiles")

	tr := tar.NewReader(r)
	for {
		hdr, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", fmt.Errorf("read tar entry: %w", readErr)
		}

		// Accept regular files only (TypeReg and deprecated TypeRegA '\x00').
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}

		entryName := filepath.Clean(hdr.Name)
		base := filepath.Base(entryName)

		// Exclude .old variants (vmlinuz.old, initrd.img.old).
		if strings.HasSuffix(base, ".old") {
			continue
		}

		isKernel := strings.HasPrefix(base, "vmlinuz")
		isInitrd := strings.HasPrefix(base, "initrd.img")
		if !isKernel && !isInitrd {
			continue
		}

		// Only extract files under boot/ or at top level.
		dir := filepath.Dir(entryName)
		if dir != "boot" && dir != "." {
			continue
		}

		var dstPath string
		if isKernel {
			dstPath = filepath.Join(workDir, digestHex+".vmlinuz")
		} else {
			dstPath = filepath.Join(workDir, digestHex+".initrd.img")
		}

		f, createErr := os.Create(dstPath) //nolint:gosec // internal temp file
		if createErr != nil {
			return "", "", fmt.Errorf("create %s: %w", filepath.Base(dstPath), createErr)
		}
		if _, copyErr := io.Copy(f, tr); copyErr != nil {
			_ = f.Close()
			return "", "", fmt.Errorf("write %s: %w", filepath.Base(dstPath), copyErr)
		}
		_ = f.Close()

		if isKernel {
			kernelPath = dstPath
		} else {
			initrdPath = dstPath
		}
		logger.Infof(ctx, "Layer %s: extracted %s", digestHex[:12], base)
	}
	return kernelPath, initrdPath, nil
}
