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
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/panjf2000/ants/v2"

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
	cached     bool
	kernelPath string // non-empty if this layer contains a kernel
	initrdPath string // non-empty if this layer contains an initrd
}

// pull downloads an OCI image, extracts boot files, and converts each layer
// to EROFS concurrently using the provided ants pool.
func pull(ctx context.Context, cfg *config.Config, pool *ants.Pool, idx *imageIndex, imageRef string) error {
	logger := log.WithFunc("oci.pull")

	parsedRef, err := name.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("invalid image reference %q: %w", imageRef, err)
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
		return fmt.Errorf("fetch image %s: %w", ref, err)
	}

	manifestDigest, err := img.Digest()
	if err != nil {
		return fmt.Errorf("get manifest digest: %w", err)
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
		return fmt.Errorf("read image index: %w", err)
	}
	if alreadyPulled {
		logger.Infof(ctx, "Already up to date: %s (digest: sha256:%s)", ref, digestHex)
		return nil
	}

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("get layers: %w", err)
	}
	if len(layers) == 0 {
		return fmt.Errorf("image %s has no layers", ref)
	}

	// Create working directory under temp.
	workDir, err := os.MkdirTemp(cfg.TempDir(), "pull-*")
	if err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir) //nolint:errcheck

	// Process layers concurrently.
	results := make([]pullLayerResult, len(layers))
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for i, layer := range layers {
		wg.Add(1)
		layerIdx := i
		layerRef := layer

		submitErr := pool.Submit(func() {
			defer wg.Done()

			if err := processLayer(ctx, cfg, layerIdx, layerRef, workDir, &results[layerIdx]); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("layer %d: %w", layerIdx, err))
				mu.Unlock()
			}
		})
		if submitErr != nil {
			wg.Done()
			mu.Lock()
			errs = append(errs, fmt.Errorf("submit layer %d: %w", layerIdx, submitErr))
			mu.Unlock()
		}
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("layer processing errors: %v", errs)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Move results and update index atomically under flock.
	// File moves must be inside the flock to prevent concurrent GC from
	// removing newly moved blobs before the index references them.
	manifestDigestFull := types.NewDigest(digestHex)
	if err := idx.Update(ctx, func(idx *imageIndex) error {
		// Double-check: another goroutine/process may have completed while we were working.
		if existing, ok := idx.Images[ref]; ok && existing.ManifestDigest == manifestDigestFull {
			return nil
		}

		var (
			layerEntries []layerEntry
			kernelLayer  types.Digest
			initrdLayer  types.Digest
		)
		for i := range results {
			r := &results[i]
			layerDigestHex := r.digest.Hex()

			// Move erofs to shared blob path (skip if already cached).
			if !r.cached {
				if err := os.Rename(r.erofsPath, cfg.BlobPath(layerDigestHex)); err != nil {
					return fmt.Errorf("move layer %d erofs: %w", r.index, err)
				}
			}

			// Move boot files to shared boot dir (skip if already cached).
			if (r.kernelPath != "" || r.initrdPath != "") && !r.cached {
				bootDir := cfg.BootDir(layerDigestHex)
				if err := os.MkdirAll(bootDir, 0o750); err != nil {
					return fmt.Errorf("create boot dir for layer %d: %w", r.index, err)
				}
				if r.kernelPath != "" {
					if err := os.Rename(r.kernelPath, filepath.Join(bootDir, "vmlinuz")); err != nil {
						return fmt.Errorf("move layer %d kernel: %w", r.index, err)
					}
				}
				if r.initrdPath != "" {
					if err := os.Rename(r.initrdPath, filepath.Join(bootDir, "initrd.img")); err != nil {
						return fmt.Errorf("move layer %d initrd: %w", r.index, err)
					}
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
			ManifestDigest: manifestDigestFull,
			Layers:         layerEntries,
			KernelLayer:    kernelLayer,
			InitrdLayer:    initrdLayer,
			CreatedAt:      time.Now().UTC(),
		}
		return nil
	}); err != nil {
		return fmt.Errorf("update image index: %w", err)
	}

	logger.Infof(ctx, "Pulled: %s (digest: sha256:%s, layers: %d)", ref, digestHex, len(layers))
	return nil
}

// processLayer handles a single layer: extracts boot files and converts to EROFS.
// If the layer's blob already exists in the shared blob store, conversion is skipped.
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
		logger.Infof(ctx, "Layer %d: sha256:%s already cached, skipping", idx, digestHex[:12])
		result.erofsPath = cfg.BlobPath(digestHex)
		result.cached = true

		// Check for cached boot files.
		bootDir := cfg.BootDir(digestHex)
		if _, err := os.Stat(filepath.Join(bootDir, "vmlinuz")); err == nil {
			result.kernelPath = filepath.Join(bootDir, "vmlinuz")
		}
		if _, err := os.Stat(filepath.Join(bootDir, "initrd.img")); err == nil {
			result.initrdPath = filepath.Join(bootDir, "initrd.img")
		}
		return nil
	}

	logger.Infof(ctx, "Layer %d: sha256:%s -> %s.erofs", idx, digestHex[:12], digestHex)

	// Extract boot files from this layer.
	// TODO: use io.TeeReader to combine boot extraction and erofs conversion into a single pass.
	kernelPath, initrdPath, extractErr := extractBootFiles(ctx, layer, workDir, digestHex)
	if extractErr != nil {
		return fmt.Errorf("extract boot files: %w", extractErr)
	}
	result.kernelPath = kernelPath
	result.initrdPath = initrdPath

	// Convert layer to EROFS using the uncompressed tar stream.
	// go-containerregistry handles decompression (gzip, zstd, etc.) transparently.
	erofsPath := filepath.Join(workDir, digestHex+".erofs")
	layerUUID := utils.UUIDv5(digestHex)

	rc, err := layer.Uncompressed()
	if err != nil {
		return fmt.Errorf("open uncompressed layer: %w", err)
	}
	defer rc.Close() //nolint:errcheck

	if err := convertLayerToErofs(ctx, rc, layerUUID, erofsPath); err != nil {
		return fmt.Errorf("convert to erofs: %w", err)
	}

	result.erofsPath = erofsPath
	return nil
}

// extractBootFiles reads a layer's tar stream and extracts kernel/initrd.
// Files are written to workDir with layer-digest-based names so no mutex is needed.
func extractBootFiles(ctx context.Context, layer v1.Layer, workDir, digestHex string) (kernelPath, initrdPath string, err error) {
	logger := log.WithFunc("oci.extractBootFiles")

	rc, err := layer.Uncompressed()
	if err != nil {
		return "", "", fmt.Errorf("open uncompressed layer: %w", err)
	}
	defer rc.Close() //nolint:errcheck

	tr := tar.NewReader(rc)
	for {
		hdr, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", fmt.Errorf("read tar entry: %w", readErr)
		}

		// Skip non-regular files (symlinks like boot/vmlinuz -> vmlinuz-6.x would
		// overwrite the real kernel with empty/invalid content).
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		entryName := filepath.Clean(hdr.Name)
		base := filepath.Base(entryName)

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
