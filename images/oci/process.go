package oci

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/images"
	"github.com/cocoonstack/cocoon/progress"
	ociProgress "github.com/cocoonstack/cocoon/progress/oci"
	"github.com/cocoonstack/cocoon/utils"
)

func processLayer(ctx context.Context, conf *Config, idx, total int, layer v1.Layer, workDir string, knownBootHexes map[string]struct{}, tracker progress.Tracker, result *pullLayerResult) error {
	logger := log.WithFunc("oci.processLayer")

	layerDigest, err := layer.Digest()
	if err != nil {
		return fmt.Errorf("get digest: %w", err)
	}
	digestHex := layerDigest.Hex

	result.index = idx
	result.digest = images.NewDigest(digestHex)

	if utils.ValidFile(conf.BlobPath(digestHex)) {
		handleCachedLayer(ctx, conf, layer, workDir, idx, total, digestHex, knownBootHexes, tracker, result)
		return nil
	}

	logger.Debugf(ctx, "Layer %d: sha256:%s -> erofs (single-pass)", idx, digestHex[:12])

	layerDir := filepath.Join(workDir, fmt.Sprintf("layer-%d", idx))
	if err = os.MkdirAll(layerDir, 0o750); err != nil {
		return fmt.Errorf("create layer work dir: %w", err)
	}

	rc, err := layer.Uncompressed()
	if err != nil {
		return fmt.Errorf("open uncompressed layer: %w", err)
	}
	defer rc.Close() //nolint:errcheck

	erofsPath := filepath.Join(layerDir, digestHex+".erofs")
	layerUUID := utils.UUIDv5(digestHex)

	cmd, erofsStdin, output, err := startErofsConversion(ctx, layerUUID, erofsPath)
	if err != nil {
		return fmt.Errorf("start erofs conversion: %w", err)
	}

	tee := io.TeeReader(rc, erofsStdin)
	kernelPath, initrdPath, scanErr := scanBootFiles(ctx, tee, layerDir, digestHex)

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

func handleCachedLayer(ctx context.Context, conf *Config, layer v1.Layer, workDir string, idx, total int, digestHex string, knownBootHexes map[string]struct{}, tracker progress.Tracker, result *pullLayerResult) {
	logger := log.WithFunc("oci.processLayer")
	logger.Debugf(ctx, "Layer %d: sha256:%s already cached", idx, digestHex[:12])
	result.erofsPath = conf.BlobPath(digestHex)

	if utils.ValidFile(conf.KernelPath(digestHex)) {
		result.kernelPath = conf.KernelPath(digestHex)
	}
	if utils.ValidFile(conf.InitrdPath(digestHex)) {
		result.initrdPath = conf.InitrdPath(digestHex)
	}

	selfHealBootFiles(ctx, conf, layer, workDir, idx, digestHex, knownBootHexes, result)

	tracker.OnEvent(ociProgress.Event{Phase: ociProgress.PhaseLayer, Index: idx, Total: total, Digest: digestHex[:12]})
}
