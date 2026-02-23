package oci

import (
	"context"
	"errors"
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
				info, statErr := os.Stat(o.conf.BlobPath(layer.Digest.Hex()))
				if statErr == nil {
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
func (o *OCI) Delete(ctx context.Context, ids []string) error {
	logger := log.WithFunc("oci.Delete")
	var errs []error
	if err := o.idx.Update(ctx, func(idx *imageIndex) error {
		for _, id := range ids {
			ref, _, ok := idx.Lookup(id)
			if !ok {
				errs = append(errs, fmt.Errorf("image %q not found", id))
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
	gcErrs := o.gcUnreferenced(ctx)
	errs = append(errs, gcErrs...)
	return errors.Join(errs...)
}

// Config generates StorageConfig and BootConfig entries for the given VMs.
// Paths are derived from layer digests at runtime, not stored in the index.
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
				configs = append(configs, &types.StorageConfig{
					Path:   o.conf.BlobPath(layer.Digest.Hex()),
					RO:     true,
					Serial: fmt.Sprintf("%s%d", serialPrefix, j),
				})
			}
			result[i] = configs

			boot[i] = &types.BootConfig{
				KernelPath: filepath.Join(o.conf.BootDir(entry.KernelLayer.Hex()), "vmlinuz"),
				InitrdPath: filepath.Join(o.conf.BootDir(entry.InitrdLayer.Hex()), "initrd.img"),
			}
		}
		return nil
	})
	return
}
