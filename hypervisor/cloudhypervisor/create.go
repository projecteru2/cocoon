package cloudhypervisor

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
)

// CowSerial is the well-known virtio serial for the COW disk attached to OCI VMs.
const CowSerial = "cocoon-cow"

// Create registers a new VM, prepares the COW disk, and persists the record.
// The VM is left in Created state — call Start to launch it.
func (ch *CloudHypervisor) Create(ctx context.Context, vmCfg *types.VMConfig, storageConfigs []*types.StorageConfig, bootCfg *types.BootConfig) (*types.VMInfo, error) {
	id := uuid.New().String()

	if err := ch.conf.EnsureCHVMDirs(id); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}

	var boot types.BootConfig
	if bootCfg != nil {
		boot = *bootCfg
	}

	// Extract blob IDs before prepare transforms the StorageConfigs.
	blobIDs := extractBlobIDs(storageConfigs, bootCfg)

	var (
		sc  []*types.StorageConfig
		err error
	)
	if boot.KernelPath != "" {
		sc, err = ch.prepareOCI(ctx, id, vmCfg, storageConfigs, &boot)
	} else {
		sc, err = ch.prepareCloudimg(ctx, id, vmCfg, storageConfigs)
	}
	if err != nil {
		return nil, err
	}

	now := time.Now()
	info := types.VMInfo{
		ID:        id,
		State:     types.VMStateCreated,
		Config:    *vmCfg,
		CreatedAt: now,
		UpdatedAt: now,
	}
	rec := hypervisor.VMRecord{
		VMInfo:         info,
		StorageConfigs: sc,
		BootConfig:     &boot,
		ImageBlobIDs:   blobIDs,
	}

	if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		idx.VMs[id] = &rec
		return nil
	}); err != nil {
		return nil, fmt.Errorf("persist VM record: %w", err)
	}

	return &info, nil
}

// prepareOCI creates a raw COW disk, appends the COW StorageConfig, and builds
// the kernel cmdline with layer/cow serial mappings.
// Returns the updated StorageConfig slice.
func (ch *CloudHypervisor) prepareOCI(ctx context.Context, vmID string, vmCfg *types.VMConfig, sc []*types.StorageConfig, boot *types.BootConfig) ([]*types.StorageConfig, error) {
	cowPath := ch.conf.CHVMCOWRawPath(vmID)
	sizeBytes := fmt.Sprintf("%d", vmCfg.Storage)

	// truncate -s <size> cow.raw
	if out, err := exec.CommandContext(ctx, "truncate", "-s", sizeBytes, cowPath).CombinedOutput(); err != nil { //nolint:gosec
		return nil, fmt.Errorf("truncate COW: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// mkfs.ext4
	if out, err := exec.CommandContext(ctx,
		"mkfs.ext4", "-F", "-m", "0", "-q",
		"-E", "lazy_itable_init=1,lazy_journal_init=1,discard",
		cowPath,
	).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mkfs.ext4 COW: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Append COW StorageConfig.
	sc = append(sc, &types.StorageConfig{
		Path:   cowPath,
		RO:     false,
		Serial: CowSerial,
	})

	// Build cmdline with reversed layer serials for overlayfs lowerdir ordering (top layer first).
	boot.Cmdline = fmt.Sprintf(
		"console=ttyS0 console=hvc0 loglevel=3 boot=cocoon cocoon.layers=%s cocoon.cow=%s clocksource=kvm-clock rw",
		strings.Join(ReverseLayerSerials(sc), ","), CowSerial,
	)

	return sc, nil
}

// prepareCloudimg creates a qcow2 COW overlay backed by the base image blob.
// Returns the updated StorageConfig slice (replaced with the overlay).
func (ch *CloudHypervisor) prepareCloudimg(ctx context.Context, vmID string, vmCfg *types.VMConfig, sc []*types.StorageConfig) ([]*types.StorageConfig, error) {
	if len(sc) == 0 {
		return nil, fmt.Errorf("cloudimg: no base image StorageConfig")
	}
	basePath := sc[0].Path
	overlayPath := ch.conf.CHVMOverlayPath(vmID)

	// qemu-img create -f qcow2 -F qcow2 -b <base> <overlay>
	if out, err := exec.CommandContext(ctx,
		"qemu-img", "create", "-f", "qcow2", "-F", "qcow2",
		"-b", basePath, overlayPath,
	).CombinedOutput(); err != nil { //nolint:gosec
		return nil, fmt.Errorf("qemu-img create overlay: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// qemu-img resize <overlay> <size>
	if vmCfg.Storage > 0 {
		sizeBytes := fmt.Sprintf("%d", vmCfg.Storage)
		if out, err := exec.CommandContext(ctx,
			"qemu-img", "resize", overlayPath, sizeBytes,
		).CombinedOutput(); err != nil { //nolint:gosec
			return nil, fmt.Errorf("qemu-img resize overlay: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	// Replace StorageConfigs with the overlay (base is accessed via backing file chain).
	return []*types.StorageConfig{{
		Path: overlayPath,
		RO:   false,
	}}, nil
}

// ReverseLayerSerials extracts read-only layer serial names from StorageConfigs
// and returns them in reverse order (top layer first for overlayfs lowerdir).
func ReverseLayerSerials(sc []*types.StorageConfig) []string {
	var serials []string
	for _, s := range sc {
		if s.RO {
			serials = append(serials, s.Serial)
		}
	}
	slices.Reverse(serials)
	return serials
}

// extractBlobIDs extracts digest hexes from the original image StorageConfigs
// and BootConfig paths. Must be called before prepare transforms them.
func extractBlobIDs(sc []*types.StorageConfig, boot *types.BootConfig) map[string]struct{} {
	ids := make(map[string]struct{})
	if boot != nil && boot.KernelPath != "" {
		// OCI: erofs layer blobs + boot dir hexes.
		for _, s := range sc {
			if s.RO {
				ids[blobHexFromPath(s.Path)] = struct{}{}
			}
		}
		// boot/{hex}/vmlinuz → hex
		ids[filepath.Base(filepath.Dir(boot.KernelPath))] = struct{}{}
		if boot.InitrdPath != "" {
			ids[filepath.Base(filepath.Dir(boot.InitrdPath))] = struct{}{}
		}
	} else if len(sc) > 0 {
		// Cloudimg: base qcow2 blob hex (before overlay replaces it).
		ids[blobHexFromPath(sc[0].Path)] = struct{}{}
	}
	return ids
}
