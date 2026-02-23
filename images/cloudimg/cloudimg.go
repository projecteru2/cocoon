package cloudimg

import (
	"context"
	"fmt"
	"os"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/progress"
	"github.com/projecteru2/cocoon/storage"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
	"github.com/projecteru2/core/log"
)

const typ = "cloudimg"

// CloudImg implements the images.Images interface using cloud images (qcow2/raw)
// downloaded from HTTP/HTTPS URLs, converted to qcow2 v3 for use with Cloud Hypervisor via UEFI boot.
type CloudImg struct {
	conf  *config.Config
	store storage.Store[imageIndex]
}

// New creates a new cloud image backend.
func New(ctx context.Context, conf *config.Config) (*CloudImg, error) {
	if err := conf.EnsureCloudimgDirs(); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}

	log.WithFunc("cloudimg.New").Infof(ctx, "cloud image backend initialized")

	return &CloudImg{
		conf:  conf,
		store: newImageStore(conf),
	}, nil
}

func (c *CloudImg) Type() string {
	return typ
}

// Pull downloads a cloud image from a URL, converts it to qcow2 v3,
// and stores the blob in the content-addressed cache.
func (c *CloudImg) Pull(ctx context.Context, url string, tracker progress.Tracker) error {
	return pull(ctx, c.conf, c.store, url, tracker)
}

// List returns all locally stored cloud images.
func (c *CloudImg) List(ctx context.Context) (result []*types.Image, err error) {
	err = c.store.With(ctx, func(idx *imageIndex) error {
		for _, entry := range idx.Images {
			blobPath := c.conf.CloudimgBlobPath(entry.ContentSum.Hex())
			var size int64
			if info, statErr := os.Stat(blobPath); statErr == nil {
				size = info.Size()
			}
			result = append(result, &types.Image{
				ID:        entry.ContentSum.String(),
				Name:      entry.Ref,
				Type:      typ,
				Size:      size,
				CreatedAt: entry.CreatedAt,
			})
		}
		return nil
	})
	return
}

// Delete removes images from the index.
// Returns the list of actually deleted refs.
func (c *CloudImg) Delete(ctx context.Context, ids []string) ([]string, error) {
	logger := log.WithFunc("cloudimg.Delete")
	var deleted []string
	return deleted, c.store.Update(ctx, func(idx *imageIndex) error {
		for _, id := range ids {
			ref, _, ok := idx.Lookup(id)
			if !ok {
				logger.Infof(ctx, "image %q not found, skipping", id)
				continue
			}
			delete(idx.Images, ref)
			deleted = append(deleted, ref)
			logger.Infof(ctx, "deleted from index: %s", ref)
		}
		return nil
	})
}

// Config generates StorageConfig and BootConfig entries for the given VMs.
// For cloud images: single qcow2 blob per image, UEFI boot (empty BootConfig).
func (c *CloudImg) Config(ctx context.Context, vms []*types.VMConfig) (result [][]*types.StorageConfig, boot []*types.BootConfig, err error) {
	err = c.store.With(ctx, func(idx *imageIndex) error {
		result = make([][]*types.StorageConfig, len(vms))
		boot = make([]*types.BootConfig, len(vms))
		for i, vm := range vms {
			_, entry, ok := idx.Lookup(vm.Image)
			if !ok {
				return fmt.Errorf("image %q not found for VM %s", vm.Image, vm.Name)
			}

			blobPath := c.conf.CloudimgBlobPath(entry.ContentSum.Hex())
			if !utils.ValidFile(blobPath) {
				return fmt.Errorf("blob invalid for VM %s (%s)", vm.Name, entry.ContentSum)
			}

			result[i] = []*types.StorageConfig{{
				Path:   blobPath,
				RO:     true,
				Serial: "cocoon-base",
			}}

			// UEFI boot: no external kernel/initrd needed.
			boot[i] = &types.BootConfig{}
		}
		return nil
	})
	return
}
