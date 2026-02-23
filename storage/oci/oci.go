package oci

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/panjf2000/ants/v2"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/core/log"
)

const (
	typ          = "oci"
	serialPrefix = "cocoon-layer"
)

// OCI implements the storage.Storage interface using OCI container images
// converted to EROFS filesystems for use with Cloud Hypervisor.
type OCI struct {
	conf *config.Config
	pool *ants.Pool
	idx  *imageIndex
}

// New creates a new OCI storage backend with an ants goroutine pool.
func New(ctx context.Context, conf *config.Config) (*OCI, error) {
	if err := conf.EnsureOCIDirs(); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}

	pool, err := ants.NewPool(conf.PoolSize)
	if err != nil {
		return nil, fmt.Errorf("create ants pool: %w", err)
	}

	log.WithFunc("oci.New").Infof(ctx, "OCI storage initialized, pool size: %d", conf.PoolSize)

	return &OCI{
		conf: conf,
		pool: pool,
		idx:  newImageIndex(conf),
	}, nil
}

// Close releases the ants goroutine pool.
func (o *OCI) Close() {
	if o.pool != nil {
		o.pool.Release()
	}
}

// Pull downloads an OCI image from a container registry, extracts boot files
// (kernel, initrd), and converts each layer to EROFS concurrently.
func (o *OCI) Pull(ctx context.Context, image string) error {
	return pull(ctx, o.conf, o.pool, o.idx, image)
}

// List returns all locally stored images.
func (o *OCI) List(ctx context.Context) (result []*types.Storage, err error) {
	err = o.idx.With(ctx, func(idx *imageIndex) error {
		for _, entry := range idx.Images {
			var totalSize int64
			for _, layer := range entry.Layers {
				if info, statErr := os.Stat(o.conf.BlobPath(layer.Digest.Hex())); statErr == nil {
					totalSize += info.Size()
				}
			}
			// Include boot file sizes (kernel + initrd).
			if entry.KernelLayer != "" {
				if info, err := os.Stat(filepath.Join(o.conf.BootDir(entry.KernelLayer.Hex()), "vmlinuz")); err == nil {
					totalSize += info.Size()
				}
			}
			if entry.InitrdLayer != "" {
				if info, err := os.Stat(filepath.Join(o.conf.BootDir(entry.InitrdLayer.Hex()), "initrd.img")); err == nil {
					totalSize += info.Size()
				}
			}
			result = append(result, &types.Storage{
				ID:        entry.ManifestDigest.String(),
				Name:      entry.Ref,
				Type:      typ,
				Size:      totalSize,
				CreatedAt: entry.CreatedAt,
			})
		}
		return nil
	})
	return
}

// Delete removes images from the index, then GCs unreferenced blobs and boot files.
// Images not found in the index are logged and skipped (idempotent delete).
func (o *OCI) Delete(ctx context.Context, ids []string) error {
	logger := log.WithFunc("oci.Delete")
	if err := o.idx.Update(ctx, func(idx *imageIndex) error {
		for _, id := range ids {
			ref, _, ok := idx.Lookup(id)
			if !ok {
				logger.Infof(ctx, "image %q not found, skipping", id)
				continue
			}
			delete(idx.Images, ref)
			logger.Infof(ctx, "Deleted from index: %s", ref)
		}
		return nil
	}); err != nil {
		return err
	}

	// GC unreferenced files. Runs in a separate flock session so the index
	// is already persisted; concurrent Pulls read the latest index.
	return o.GC(ctx)
}

// Config generates StorageConfig and BootConfig entries for the given VMs.
// Paths are derived from layer digests at runtime, not stored in the index.
// Returns an error if any referenced blob or boot file is missing on disk.
func (o *OCI) Config(ctx context.Context, vms []*types.VMConfig) (result [][]*types.StorageConfig, boot []*types.BootConfig, err error) {
	err = o.idx.With(ctx, func(idx *imageIndex) error {
		result = make([][]*types.StorageConfig, len(vms))
		boot = make([]*types.BootConfig, len(vms))
		for i, vm := range vms {
			entry, ok := idx.Images[vm.Image]
			if !ok {
				return fmt.Errorf("image %q not found for VM %s", vm.Image, vm.Name)
			}

			var configs []*types.StorageConfig
			for j, layer := range entry.Layers {
				blobPath := o.conf.BlobPath(layer.Digest.Hex())
				if _, err := os.Stat(blobPath); err != nil {
					return fmt.Errorf("blob missing for VM %s layer %d (%s): %w", vm.Name, j, layer.Digest, err)
				}
				configs = append(configs, &types.StorageConfig{
					Path:   blobPath,
					RO:     true,
					Serial: fmt.Sprintf("%s%d", serialPrefix, j),
				})
			}
			result[i] = configs

			kernelPath := filepath.Join(o.conf.BootDir(entry.KernelLayer.Hex()), "vmlinuz")
			initrdPath := filepath.Join(o.conf.BootDir(entry.InitrdLayer.Hex()), "initrd.img")
			if _, err := os.Stat(kernelPath); err != nil {
				return fmt.Errorf("kernel missing for VM %s (%s): %w", vm.Name, entry.KernelLayer, err)
			}
			if _, err := os.Stat(initrdPath); err != nil {
				return fmt.Errorf("initrd missing for VM %s (%s): %w", vm.Name, entry.InitrdLayer, err)
			}
			boot[i] = &types.BootConfig{
				KernelPath: kernelPath,
				InitrdPath: initrdPath,
			}
		}
		return nil
	})
	return
}
