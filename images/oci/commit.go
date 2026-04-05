package oci

import (
	"fmt"
	"os"
	"time"

	"github.com/cocoonstack/cocoon/images"
	"github.com/cocoonstack/cocoon/utils"
)

func moveBootFile(src, dst, bootDir string, layerIdx int, name string) error {
	if src == "" || src == dst {
		return nil
	}
	if err := os.MkdirAll(bootDir, 0o750); err != nil {
		return fmt.Errorf("create boot dir for layer %d: %w", layerIdx, err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("move layer %d %s: %w", layerIdx, name, err)
	}
	return nil
}

func bootFilesPresent(results []pullLayerResult) (hasKernel, hasInitrd bool) {
	for i := range results {
		if results[i].kernelPath != "" {
			hasKernel = true
		}
		if results[i].initrdPath != "" {
			hasInitrd = true
		}
		if hasKernel && hasInitrd {
			return
		}
	}
	return
}

func commitAndRecord(conf *Config, idx *imageIndex, ref string, manifestDigest images.Digest, results []pullLayerResult) error {
	var (
		layerEntries []layerEntry
		kernelLayer  images.Digest
		initrdLayer  images.Digest
	)

	for i := range results {
		r := &results[i]
		layerDigestHex := r.digest.Hex()

		if r.erofsPath != conf.BlobPath(layerDigestHex) {
			if err := os.Rename(r.erofsPath, conf.BlobPath(layerDigestHex)); err != nil {
				return fmt.Errorf("move layer %d erofs: %w", r.index, err)
			}
		}

		if err := moveBootFile(r.kernelPath, conf.KernelPath(layerDigestHex), conf.BootDir(layerDigestHex), r.index, "kernel"); err != nil {
			return err
		}
		if err := moveBootFile(r.initrdPath, conf.InitrdPath(layerDigestHex), conf.BootDir(layerDigestHex), r.index, "initrd"); err != nil {
			return err
		}

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

	var totalSize int64
	for _, le := range layerEntries {
		if info, err := os.Stat(conf.BlobPath(le.Digest.Hex())); err == nil {
			totalSize += info.Size()
		}
	}
	if info, err := os.Stat(conf.KernelPath(kernelLayer.Hex())); err == nil {
		totalSize += info.Size()
	}
	if info, err := os.Stat(conf.InitrdPath(initrdLayer.Hex())); err == nil {
		totalSize += info.Size()
	}

	idx.Images[ref] = &imageEntry{
		Ref:            ref,
		ManifestDigest: manifestDigest,
		Layers:         layerEntries,
		KernelLayer:    kernelLayer,
		InitrdLayer:    initrdLayer,
		Size:           totalSize,
		CreatedAt:      time.Now(),
	}
	return nil
}
