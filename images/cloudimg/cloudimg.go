package cloudimg

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/sync/singleflight"

	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/progress"
	"github.com/projecteru2/cocoon/storage"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

const typ = "cloudimg"

// CloudImg implements the images.Images interface using cloud images (qcow2/raw)
// downloaded from HTTP/HTTPS URLs, converted to qcow2 v3 for use with Cloud Hypervisor via UEFI boot.
type CloudImg struct {
	conf      *config.Config
	store     storage.Store[imageIndex]
	locker    lock.Locker
	pullGroup singleflight.Group
}

// New creates a new cloud image backend.
func New(ctx context.Context, conf *config.Config) (*CloudImg, error) {
	if err := conf.EnsureCloudimgDirs(); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}

	log.WithFunc("cloudimg.New").Info(ctx, "cloud image backend initialized")

	store, locker := images.NewStore[imageIndex](conf.CloudimgIndexFile(), conf.CloudimgIndexLock())
	return &CloudImg{
		conf:   conf,
		store:  store,
		locker: locker,
	}, nil
}

func (c *CloudImg) Type() string { return typ }

// Pull downloads a cloud image from a URL, converts it to qcow2 v3,
// and stores the blob in the content-addressed cache.
func (c *CloudImg) Pull(ctx context.Context, url string, tracker progress.Tracker) error {
	_, err, _ := c.pullGroup.Do(url, func() (any, error) {
		return nil, pull(ctx, c.conf, c.store, url, tracker)
	})
	return err
}

// Inspect returns the record for a single image. Returns (nil, nil) if not found.
func (c *CloudImg) Inspect(ctx context.Context, id string) (*types.Image, error) {
	return images.InspectEntry(ctx, c.store, id, typ,
		func(idx *imageIndex, q string) []string { return idx.LookupRefs(q) },
		func(idx *imageIndex) map[string]*imageEntry { return idx.Images },
		c.imageSizer,
	)
}

// List returns all locally stored cloud images.
func (c *CloudImg) List(ctx context.Context) ([]*types.Image, error) {
	return images.ListEntries(ctx, c.store, typ,
		func(idx *imageIndex) map[string]*imageEntry { return idx.Images },
		c.imageSizer,
	)
}

// Delete removes images from the index.
// Returns the list of actually deleted refs.
func (c *CloudImg) Delete(ctx context.Context, ids []string) ([]string, error) {
	return images.DeleteEntries(ctx, c.store, "cloudimg.Delete", ids,
		func(idx *imageIndex) map[string]*imageEntry { return idx.Images },
		func(idx *imageIndex, q string) []string { return idx.LookupRefs(q) },
	)
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

			firmwarePath := c.conf.FirmwarePath()
			if !utils.ValidFile(firmwarePath) {
				return fmt.Errorf("firmware not found: %s", firmwarePath)
			}
			boot[i] = &types.BootConfig{
				FirmwarePath: firmwarePath,
			}
		}
		return nil
	})
	return result, boot, err
}

func (c *CloudImg) imageSizer(e *imageEntry) int64 {
	if info, err := os.Stat(c.conf.CloudimgBlobPath(e.ContentSum.Hex())); err == nil {
		return info.Size()
	}
	return 0
}
