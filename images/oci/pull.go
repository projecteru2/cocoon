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
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/progress"
	ociProgress "github.com/projecteru2/cocoon/progress/oci"
	"github.com/projecteru2/cocoon/storage"
	"github.com/projecteru2/cocoon/utils"
	"github.com/projecteru2/core/log"
)

// pullLayerResult holds the output of processing a single layer.
type pullLayerResult struct {
	index      int
	digest     images.Digest
	erofsPath  string
	kernelPath string // non-empty if this layer contains a kernel
	initrdPath string // non-empty if this layer contains an initrd
}

// pull downloads an OCI image, extracts boot files, and converts each layer
// to EROFS concurrently using errgroup.
func pull(ctx context.Context, conf *config.Config, store storage.Store[imageIndex], imageRef string, tracker progress.Tracker) error {
	logger := log.WithFunc("oci.pull")

	ref, digestHex, workDir, results, err := fetchAndProcess(ctx, conf, store, imageRef, tracker)
	if err != nil {
		return err
	}
	if results == nil {
		logger.Infof(ctx, "Already up to date: %s (digest: sha256:%s)", ref, digestHex)
		return nil
	}
	// Clean up workDir after commit (not before).
	defer os.RemoveAll(workDir) //nolint:errcheck

	// Commit artifacts and update index atomically under flock.
	// No digest-only short-circuit here: fetchAndProcess proceeds even when the
	// digest matches but local files are invalid, so commitAndRecord must always
	// run to move repaired artifacts into place. commitAndRecord itself is
	// idempotent (skips rename when src == dst).
	tracker.OnEvent(ociProgress.Event{Phase: ociProgress.PhaseCommit, Index: -1, Total: len(results)})
	manifestDigest := images.NewDigest(digestHex)
	if err := store.Update(ctx, func(idx *imageIndex) error {
		return commitAndRecord(conf, idx, ref, manifestDigest, results)
	}); err != nil {
		return fmt.Errorf("update image index: %w", err)
	}

	tracker.OnEvent(ociProgress.Event{Phase: ociProgress.PhaseDone, Index: -1, Total: len(results)})
	logger.Infof(ctx, "Pulled: %s (digest: sha256:%s, layers: %d)", ref, digestHex, len(results))
	return nil
}

// fetchAndProcess downloads the image and processes all layers concurrently.
// Returns nil results if the image is already up-to-date.
// The caller owns workDir cleanup via the returned path (empty when already up-to-date).
func fetchAndProcess(ctx context.Context, conf *config.Config, store storage.Store[imageIndex], imageRef string, tracker progress.Tracker) (ref, digestHex, workDir string, results []pullLayerResult, err error) {
	logger := log.WithFunc("oci.pull")

	parsedRef, parseErr := name.ParseReference(imageRef)
	if parseErr != nil {
		return "", "", "", nil, fmt.Errorf("invalid image reference %q: %w", imageRef, parseErr)
	}
	ref = parsedRef.String()

	platform := v1.Platform{
		Architecture: runtime.GOARCH,
		OS:           runtime.GOOS,
	}

	logger.Infof(ctx, "Pulling image: %s", ref)

	img, fetchErr := remote.Image(parsedRef,
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithContext(ctx),
		remote.WithPlatform(platform),
	)
	if fetchErr != nil {
		return "", "", "", nil, fmt.Errorf("fetch image %s: %w", ref, fetchErr)
	}

	manifest, digestErr := img.Digest()
	if digestErr != nil {
		return "", "", "", nil, fmt.Errorf("get manifest digest: %w", digestErr)
	}
	digestHex = manifest.Hex

	// Idempotency: check if already pulled with same manifest and all files intact.
	// Also collect known boot layer digests so processLayer can target self-heal
	// even when the boot directory has been entirely deleted.
	var alreadyPulled bool
	knownBootHexes := make(map[string]struct{})
	if withErr := store.With(ctx, func(idx *imageIndex) error {
		// Collect boot layer digests from ALL entries for cross-image self-heal.
		// This ensures processLayer can recover boot files even when the current
		// ref has no prior index record (e.g., first pull sharing cached layers).
		for _, e := range idx.Images {
			if e == nil {
				continue
			}
			if e.KernelLayer != "" {
				knownBootHexes[e.KernelLayer.Hex()] = struct{}{}
			}
			if e.InitrdLayer != "" {
				knownBootHexes[e.InitrdLayer.Hex()] = struct{}{}
			}
		}

		// Idempotency check: same ref and manifest digest with all files intact.
		entry, ok := idx.Images[ref]
		if !ok || entry == nil || entry.ManifestDigest != images.NewDigest(digestHex) {
			return nil
		}
		if !utils.ValidFile(conf.KernelPath(entry.KernelLayer.Hex())) ||
			!utils.ValidFile(conf.InitrdPath(entry.InitrdLayer.Hex())) {
			return nil
		}
		for _, layer := range entry.Layers {
			if !utils.ValidFile(conf.BlobPath(layer.Digest.Hex())) {
				return nil
			}
		}
		alreadyPulled = true
		return nil
	}); withErr != nil {
		return "", "", "", nil, fmt.Errorf("read image index: %w", withErr)
	}
	if alreadyPulled {
		return ref, digestHex, "", nil, nil
	}

	layers, layersErr := img.Layers()
	if layersErr != nil {
		return "", "", "", nil, fmt.Errorf("get layers: %w", layersErr)
	}
	if len(layers) == 0 {
		return "", "", "", nil, fmt.Errorf("image %s has no layers", ref)
	}

	tracker.OnEvent(ociProgress.Event{Phase: ociProgress.PhasePull, Index: -1, Total: len(layers)})

	// Create working directory under temp. Caller is responsible for cleanup.
	workDir, mkErr := os.MkdirTemp(conf.OCITempDir(), "pull-*")
	if mkErr != nil {
		return "", "", "", nil, fmt.Errorf("create work dir: %w", mkErr)
	}

	// Process layers concurrently with bounded parallelism.
	results = make([]pullLayerResult, len(layers))
	g, gctx := errgroup.WithContext(ctx)
	limit := conf.PoolSize
	if limit <= 0 {
		limit = runtime.NumCPU()
	}
	g.SetLimit(limit)

	totalLayers := len(layers)
	for i, layer := range layers {
		layerIdx := i
		layerRef := layer
		g.Go(func() error {
			return processLayer(gctx, conf, layerIdx, totalLayers, layerRef, workDir, knownBootHexes, tracker, &results[layerIdx])
		})
	}

	if waitErr := g.Wait(); waitErr != nil {
		os.RemoveAll(workDir) //nolint:errcheck
		return "", "", "", nil, fmt.Errorf("process layers: %w", waitErr)
	}

	healCachedBootFiles(ctx, conf, layers, results, workDir)

	return ref, digestHex, workDir, results, nil
}

// commitAndRecord moves artifacts to shared image paths and records the image entry.
// Must be called under flock (inside idx.Update).
func commitAndRecord(conf *config.Config, idx *imageIndex, ref string, manifestDigest images.Digest, results []pullLayerResult) error {
	var (
		layerEntries []layerEntry
		kernelLayer  images.Digest
		initrdLayer  images.Digest
	)

	for i := range results {
		r := &results[i]
		layerDigestHex := r.digest.Hex()

		// Move erofs to shared blob path if not already there.
		if r.erofsPath != conf.BlobPath(layerDigestHex) {
			if err := os.Rename(r.erofsPath, conf.BlobPath(layerDigestHex)); err != nil {
				return fmt.Errorf("move layer %d erofs: %w", r.index, err)
			}
		}

		// Move boot files to shared boot dir if not already there.
		if r.kernelPath != "" && r.kernelPath != conf.KernelPath(layerDigestHex) {
			if err := os.MkdirAll(conf.BootDir(layerDigestHex), 0o750); err != nil {
				return fmt.Errorf("create boot dir for layer %d: %w", r.index, err)
			}
			if err := os.Rename(r.kernelPath, conf.KernelPath(layerDigestHex)); err != nil {
				return fmt.Errorf("move layer %d kernel: %w", r.index, err)
			}
		}
		if r.initrdPath != "" && r.initrdPath != conf.InitrdPath(layerDigestHex) {
			if err := os.MkdirAll(conf.BootDir(layerDigestHex), 0o750); err != nil {
				return fmt.Errorf("create boot dir for layer %d: %w", r.index, err)
			}
			if err := os.Rename(r.initrdPath, conf.InitrdPath(layerDigestHex)); err != nil {
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

	// Final validation: ensure all artifacts still exist on disk.
	// Guards against concurrent GC deleting cached blobs/boot files between
	// processLayer (no flock) and this point (under flock).
	for _, le := range layerEntries {
		if !utils.ValidFile(conf.BlobPath(le.Digest.Hex())) {
			return fmt.Errorf("blob missing for layer %s (concurrent GC?)", le.Digest)
		}
	}
	if !utils.ValidFile(conf.KernelPath(kernelLayer.Hex())) {
		return fmt.Errorf("kernel missing for %s (concurrent GC?)", kernelLayer)
	}
	if !utils.ValidFile(conf.InitrdPath(initrdLayer.Hex())) {
		return fmt.Errorf("initrd missing for %s (concurrent GC?)", initrdLayer)
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

// healCachedBootFiles force-scans cached layers to recover missing boot files.
// processLayer's first-pass self-heal is evidence-based: it only re-extracts
// boot files when it has positive evidence (existing boot file, boot dir on
// disk, or knownBootHexes from the index). This leaves a blind spot when all
// evidence has been erased (every entry referencing the boot layer deleted and
// GC removed the boot dir) but the erofs blob is still cached.
// healCachedBootFiles closes that gap by running after all layers are processed:
// if kernel or initrd (or both) are still missing across results, it sequentially
// re-scans every cached layer to find them.
func healCachedBootFiles(ctx context.Context, conf *config.Config, layers []v1.Layer, results []pullLayerResult, workDir string) {
	logger := log.WithFunc("oci.healCachedBootFiles")

	var hasKernel, hasInitrd bool
	for i := range results {
		if results[i].kernelPath != "" {
			hasKernel = true
		}
		if results[i].initrdPath != "" {
			hasInitrd = true
		}
	}
	if hasKernel && hasInitrd {
		return
	}

	logger.Warnf(ctx, "Boot files incomplete after first pass (kernel=%v, initrd=%v), force-scanning cached layers", hasKernel, hasInitrd)

	for i, layer := range layers {
		digestHex := results[i].digest.Hex()
		// Only re-scan cached layers; freshly processed layers were already fully scanned.
		if results[i].erofsPath != conf.BlobPath(digestHex) {
			continue
		}
		healDir := filepath.Join(workDir, fmt.Sprintf("heal-%d", i))
		if mkErr := os.MkdirAll(healDir, 0o750); mkErr != nil {
			logger.Warnf(ctx, "Layer %d: cannot create heal dir: %v", i, mkErr)
			continue
		}
		rc, rcErr := layer.Uncompressed()
		if rcErr != nil {
			logger.Warnf(ctx, "Layer %d: cannot open for boot scan: %v", i, rcErr)
			continue
		}
		kp, ip, scanErr := scanBootFiles(ctx, rc, healDir, digestHex)
		_ = rc.Close()
		if scanErr != nil {
			logger.Warnf(ctx, "Layer %d: boot scan failed: %v", i, scanErr)
			continue
		}
		if results[i].kernelPath == "" && kp != "" {
			results[i].kernelPath = kp
		}
		if results[i].initrdPath == "" && ip != "" {
			results[i].initrdPath = ip
		}
	}
}

// processLayer handles a single layer: extracts boot files and converts to EROFS
// in a single pass using io.TeeReader. If the layer is already cached, it checks
// for missing boot files and self-heals by re-extracting them.
// knownBootHexes contains digest hex strings of layers previously recorded as boot
// layers in the index, enabling targeted self-heal even when bootDir is deleted.
func processLayer(ctx context.Context, conf *config.Config, idx, total int, layer v1.Layer, workDir string, knownBootHexes map[string]struct{}, tracker progress.Tracker, result *pullLayerResult) error {
	logger := log.WithFunc("oci.processLayer")

	layerDigest, err := layer.Digest()
	if err != nil {
		return fmt.Errorf("get digest: %w", err)
	}
	digestHex := layerDigest.Hex

	result.index = idx
	result.digest = images.NewDigest(digestHex)

	// Check if this layer's blob already exists and is valid (shared across images).
	if utils.ValidFile(conf.BlobPath(digestHex)) {
		logger.Infof(ctx, "Layer %d: sha256:%s already cached", idx, digestHex[:12])
		result.erofsPath = conf.BlobPath(digestHex)

		// Check for cached boot files (must exist and be non-empty).
		if utils.ValidFile(conf.KernelPath(digestHex)) {
			result.kernelPath = conf.KernelPath(digestHex)
		}
		if utils.ValidFile(conf.InitrdPath(digestHex)) {
			result.initrdPath = conf.InitrdPath(digestHex)
		}

		// Best-effort self-heal: re-extract missing/invalid boot files.
		// Evidence sources (any one suffices): existing boot file, boot dir on
		// disk, or index records this digest as a boot layer. The last source
		// covers the "bootDir entirely deleted" scenario without scanning every
		// non-boot layer. Failures are logged, not fatal — commitAndRecord
		// validates at image level.
		hasBootEvidence := result.kernelPath != "" || result.initrdPath != ""
		if !hasBootEvidence {
			_, statErr := os.Stat(conf.BootDir(digestHex))
			hasBootEvidence = statErr == nil
		}
		if !hasBootEvidence {
			_, hasBootEvidence = knownBootHexes[digestHex]
		}
		if hasBootEvidence && (result.kernelPath == "" || result.initrdPath == "") {
			logger.Warnf(ctx, "Layer %d: sha256:%s attempting boot file recovery", idx, digestHex[:12])
			healDir := filepath.Join(workDir, fmt.Sprintf("heal-%d", idx))
			if mkErr := os.MkdirAll(healDir, 0o750); mkErr != nil {
				logger.Warnf(ctx, "Layer %d: cannot create heal dir: %v", idx, mkErr)
			} else if rc, rcErr := layer.Uncompressed(); rcErr != nil {
				logger.Warnf(ctx, "Layer %d: cannot recover boot files: %v", idx, rcErr)
			} else {
				kp, ip, scanErr := scanBootFiles(ctx, rc, healDir, digestHex)
				_ = rc.Close()
				if scanErr != nil {
					logger.Warnf(ctx, "Layer %d: boot file recovery failed: %v", idx, scanErr)
				} else {
					if result.kernelPath == "" {
						result.kernelPath = kp
					}
					if result.initrdPath == "" {
						result.initrdPath = ip
					}
				}
			}
		}
		tracker.OnEvent(ociProgress.Event{Phase: ociProgress.PhaseLayer, Index: idx, Total: total, Digest: digestHex[:12]})
		return nil
	}

	logger.Infof(ctx, "Layer %d: sha256:%s -> erofs (single-pass)", idx, digestHex[:12])

	// Per-layer work subdirectory avoids temp file conflicts when
	// a manifest references the same digest more than once.
	layerDir := filepath.Join(workDir, fmt.Sprintf("layer-%d", idx))
	if err := os.MkdirAll(layerDir, 0o750); err != nil {
		return fmt.Errorf("create layer work dir: %w", err)
	}

	// Open uncompressed tar stream once.
	rc, err := layer.Uncompressed()
	if err != nil {
		return fmt.Errorf("open uncompressed layer: %w", err)
	}
	defer rc.Close() //nolint:errcheck

	erofsPath := filepath.Join(layerDir, digestHex+".erofs")
	layerUUID := utils.UUIDv5(digestHex)

	// Start mkfs.erofs in background, receiving the tar stream via pipe.
	cmd, erofsStdin, output, err := startErofsConversion(ctx, layerUUID, erofsPath)
	if err != nil {
		return fmt.Errorf("start erofs conversion: %w", err)
	}

	// TeeReader: every byte read for boot scanning also feeds mkfs.erofs.
	tee := io.TeeReader(rc, erofsStdin)
	kernelPath, initrdPath, scanErr := scanBootFiles(ctx, tee, layerDir, digestHex)

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
	tracker.OnEvent(ociProgress.Event{Phase: ociProgress.PhaseLayer, Index: idx, Total: total, Digest: digestHex[:12]})
	return nil
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
