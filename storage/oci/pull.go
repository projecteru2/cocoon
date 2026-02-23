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
	"github.com/projecteru2/cocoon/utils"
	"github.com/projecteru2/core/log"
)

// pullLayerResult holds the output of processing a single layer.
type pullLayerResult struct {
	index      int
	digest     string
	erofsPath  string
	cached     bool
	kernelPath string // non-empty if this layer contains a kernel
	initrdPath string // non-empty if this layer contains an initrd
}

// pull downloads an OCI image, extracts boot files, and converts each layer
// to EROFS concurrently using the provided ants pool. This mirrors the flow
// in os-image/start.sh step 4.
func pull(ctx context.Context, cfg *config.Config, pool *ants.Pool, idx *imageIndex, imageRef string) error {
	logger := log.WithFunc("oci.pull")

	ref, err := normalizeRef(imageRef)
	if err != nil {
		return err
	}

	tag, err := name.NewTag(ref)
	if err != nil {
		return fmt.Errorf("invalid image reference %q: %w", ref, err)
	}

	platform := v1.Platform{
		Architecture: runtime.GOARCH,
		OS:           "linux",
	}

	logger.Infof(ctx, "Pulling image: %s", ref)

	img, err := remote.Image(tag,
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
		if entry, ok := idx.Images[ref]; ok && entry.ManifestDigest == "sha256:"+digestHex {
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

	// Prepare image directory path (may not exist yet for new images).
	imageDir := cfg.ImageDir(digestHex)

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

			if err := processLayer(ctx, cfg, imageDir, layerIdx, layerRef, workDir, &results[layerIdx]); err != nil {
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

	// Move results to final image directory.
	if err := os.MkdirAll(imageDir, 0o750); err != nil {
		return fmt.Errorf("create image dir: %w", err)
	}

	var (
		layerEntries []layerEntry
		kernelPath   string
		initrdPath   string
	)
	for i := range results {
		r := &results[i]
		layerDigestHex := strings.TrimPrefix(r.digest, "sha256:")

		// Move erofs file.
		finalErofsPath := filepath.Join(imageDir, layerDigestHex+".erofs")
		if !r.cached {
			if err := os.Rename(r.erofsPath, finalErofsPath); err != nil {
				return fmt.Errorf("move layer %d erofs: %w", r.index, err)
			}
		}

		// Move boot files to /boot/{layerDigestHex}/ for easy lookup and cross-image reuse.
		if (r.kernelPath != "" || r.initrdPath != "") && !r.cached {
			bootDir := cfg.BootDir(layerDigestHex)
			if err := os.MkdirAll(bootDir, 0o750); err != nil {
				return fmt.Errorf("create boot dir for layer %d: %w", r.index, err)
			}
			if r.kernelPath != "" {
				dst := filepath.Join(bootDir, "vmlinuz")
				if err := os.Rename(r.kernelPath, dst); err != nil {
					return fmt.Errorf("move layer %d kernel: %w", r.index, err)
				}
				r.kernelPath = dst
			}
			if r.initrdPath != "" {
				dst := filepath.Join(bootDir, "initrd.img")
				if err := os.Rename(r.initrdPath, dst); err != nil {
					return fmt.Errorf("move layer %d initrd: %w", r.index, err)
				}
				r.initrdPath = dst
			}
		}

		// Later layers override earlier ones (OCI layer ordering).
		if r.kernelPath != "" {
			kernelPath = r.kernelPath
		}
		if r.initrdPath != "" {
			initrdPath = r.initrdPath
		}

		layerEntries = append(layerEntries, layerEntry{
			Digest:    r.digest,
			ErofsPath: finalErofsPath,
		})
	}

	if kernelPath == "" || initrdPath == "" {
		return fmt.Errorf("image %s has no boot files (vmlinuz/initrd.img)", ref)
	}

	// Update image index (flock-protected).
	entry := &imageEntry{
		Ref:            ref,
		ManifestDigest: "sha256:" + digestHex,
		Layers:         layerEntries,
		KernelPath:     kernelPath,
		InitrdPath:     initrdPath,
		CreatedAt:      time.Now().UTC(),
	}

	if err := idx.Update(ctx, func(idx *imageIndex) error {
		idx.Images[ref] = entry
		return nil
	}); err != nil {
		return fmt.Errorf("update image index: %w", err)
	}

	logger.Infof(ctx, "Pulled: %s (digest: sha256:%s, layers: %d)", ref, digestHex, len(layers))
	return nil
}

// processLayer handles a single layer: extracts boot files and converts to EROFS.
// If the layer's erofs file already exists in imageDir, the conversion is skipped
// and cached boot files are detected automatically.
func processLayer(ctx context.Context, cfg *config.Config, imageDir string, idx int, layer v1.Layer, workDir string, result *pullLayerResult) error {
	logger := log.WithFunc("oci.processLayer")

	layerDigest, err := layer.Digest()
	if err != nil {
		return fmt.Errorf("get digest: %w", err)
	}
	digestHex := layerDigest.Hex

	result.index = idx
	result.digest = "sha256:" + digestHex

	// Check if this layer's erofs file already exists (e.g. from a previous interrupted pull).
	existingPath := filepath.Join(imageDir, digestHex+".erofs")
	if _, statErr := os.Stat(existingPath); statErr == nil {
		logger.Infof(ctx, "Layer %d: sha256:%s already cached, skipping", idx, digestHex[:12])
		result.erofsPath = existingPath
		result.cached = true

		// Check for cached boot files in boot directory.
		bootDir := cfg.BootDir(digestHex)
		if _, err := os.Stat(filepath.Join(bootDir, "vmlinuz")); err == nil {
			result.kernelPath = filepath.Join(bootDir, "vmlinuz")
		}
		if _, err := os.Stat(filepath.Join(bootDir, "initrd.img")); err == nil {
			result.initrdPath = filepath.Join(bootDir, "initrd.img")
		}
		return nil
	}

	mediaType, err := layer.MediaType()
	if err != nil {
		return fmt.Errorf("get media type: %w", err)
	}
	isGzip := strings.Contains(string(mediaType), "gzip")

	logger.Infof(ctx, "Layer %d: sha256:%s -> %s.erofs", idx, digestHex[:12], digestHex)

	// Extract boot files from this layer (each layer writes to unique paths, no mutex needed).
	result.kernelPath, result.initrdPath = extractBootFiles(ctx, layer, workDir, digestHex)

	// Convert layer to EROFS.
	erofsPath := filepath.Join(workDir, digestHex+".erofs")
	layerUUID := utils.UUIDv5(digestHex)

	rc, err := layer.Compressed()
	if err != nil {
		return fmt.Errorf("open compressed layer: %w", err)
	}
	defer rc.Close() //nolint:errcheck

	if err := convertLayerToErofs(ctx, rc, isGzip, layerUUID, erofsPath); err != nil {
		return fmt.Errorf("convert to erofs: %w", err)
	}

	result.erofsPath = erofsPath
	return nil
}

// extractBootFiles reads a layer's tar stream and extracts kernel/initrd.
// Files are written to workDir with layer-digest-based names so no mutex is needed.
// Returns the paths of extracted files (empty if not found).
func extractBootFiles(ctx context.Context, layer v1.Layer, workDir, digestHex string) (kernelPath, initrdPath string) {
	logger := log.WithFunc("oci.extractBootFiles")

	rc, err := layer.Uncompressed()
	if err != nil {
		return
	}
	defer rc.Close() //nolint:errcheck

	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
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
			logger.Errorf(ctx, createErr, "Layer %s: create boot file %s", digestHex[:12], dstPath)
			return
		}
		if _, copyErr := io.Copy(f, tr); copyErr != nil {
			_ = f.Close()
			logger.Errorf(ctx, copyErr, "Layer %s: write boot file %s", digestHex[:12], dstPath)
			return
		}
		_ = f.Close()

		if isKernel {
			kernelPath = dstPath
		} else {
			initrdPath = dstPath
		}
	}
	return
}
