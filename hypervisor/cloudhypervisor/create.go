package cloudhypervisor

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/metadata"
	"github.com/projecteru2/cocoon/types"
)

// CowSerial is the well-known virtio serial for the COW disk attached to OCI VMs.
const CowSerial = "cocoon-cow"

// Create registers a new VM, prepares the COW disk, and persists the record.
// The VM is left in Created state — call Start to launch it.
//
// To avoid a race with GC (which scans directories and removes those not in
// the DB), we write a placeholder record first, then create directories and
// prepare disks, and finally update the record to Created state.
func (ch *CloudHypervisor) Create(ctx context.Context, vmCfg *types.VMConfig, storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig, bootCfg *types.BootConfig) (*types.VM, error) {
	id := hypervisor.GenerateID()
	now := time.Now()

	blobIDs := extractBlobIDs(storageConfigs, bootCfg)

	// Step 1: write a placeholder record so GC won't treat our dirs as orphans.
	if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		if idx.VMs[id] != nil {
			return fmt.Errorf("ID collision %q (retry)", id)
		}
		if dup, ok := idx.Names[vmCfg.Name]; ok {
			return fmt.Errorf("VM name %q already exists (id: %s)", vmCfg.Name, dup)
		}
		idx.VMs[id] = &hypervisor.VMRecord{
			VM: types.VM{
				ID: id, State: types.VMStateCreating,
				Config: *vmCfg, CreatedAt: now, UpdatedAt: now,
			},
			ImageBlobIDs: blobIDs,
		}
		idx.Names[vmCfg.Name] = id
		return nil
	}); err != nil {
		return nil, fmt.Errorf("reserve VM record: %w", err)
	}

	// Step 2: create directories and prepare disks.
	if err := ch.conf.EnsureCHVMDirs(id); err != nil {
		ch.rollbackCreate(ctx, id, vmCfg.Name)
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}

	var (
		sc       []*types.StorageConfig
		bootCopy *types.BootConfig
		err      error
	)
	if bootCfg != nil {
		b := *bootCfg
		bootCopy = &b
	}
	if bootCopy != nil && bootCopy.KernelPath != "" {
		sc, err = ch.prepareOCI(ctx, id, vmCfg, storageConfigs, networkConfigs, bootCopy)
	} else {
		sc, err = ch.prepareCloudimg(ctx, id, vmCfg, storageConfigs, networkConfigs)
	}
	if err != nil {
		_ = ch.removeVMDirs(ctx, id)
		ch.rollbackCreate(ctx, id, vmCfg.Name)
		return nil, err
	}

	// Step 3: finalize the record with full data and Created state.
	info := types.VM{
		ID: id, State: types.VMStateCreated,
		Config: *vmCfg, CreatedAt: now, UpdatedAt: now,
	}
	rec := hypervisor.VMRecord{
		VM:             info,
		StorageConfigs: sc,
		NetworkConfigs: networkConfigs,
		BootConfig:     bootCopy,
		ImageBlobIDs:   blobIDs,
	}
	if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		idx.VMs[id] = &rec
		return nil
	}); err != nil {
		_ = ch.removeVMDirs(ctx, id)
		ch.rollbackCreate(ctx, id, vmCfg.Name)
		return nil, fmt.Errorf("finalize VM record: %w", err)
	}

	return &info, nil
}

// prepareOCI creates a raw COW disk, appends the COW StorageConfig, and builds
// the kernel cmdline with layer/cow serial mappings.
// Returns the updated StorageConfig slice.
func (ch *CloudHypervisor) prepareOCI(ctx context.Context, vmID string, vmCfg *types.VMConfig, sc []*types.StorageConfig, nc []*types.NetworkConfig, boot *types.BootConfig) ([]*types.StorageConfig, error) {
	cowPath := ch.conf.CHVMCOWRawPath(vmID)

	// Create sparse COW file (equivalent to truncate -s <size>).
	if err := os.Truncate(cowPath, vmCfg.Storage); err != nil {
		return nil, fmt.Errorf("truncate COW: %w", err)
	}
	// mkfs.ext4
	if out, err := exec.CommandContext(ctx, //nolint:gosec
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
	var cmdline strings.Builder
	fmt.Fprintf(&cmdline,
		"console=hvc0 loglevel=3 boot=cocoon cocoon.layers=%s cocoon.cow=%s clocksource=kvm-clock rw",
		strings.Join(ReverseLayerSerials(sc), ","), CowSerial,
	)

	// Append static IP configuration for each network interface.
	// Format: ip=<client-IP>:<server>:<gw-IP>:<netmask>:<hostname>:<device>:<autoconf>
	if len(nc) > 0 {
		cmdline.WriteString(" net.ifnames=0")
		for _, n := range nc {
			if n.Network != nil {
				fmt.Fprintf(&cmdline, " ip=%s::%s:%s::%s:off",
					n.Network.IP, n.Network.Gateway,
					net.IP(n.Network.Netmask), n.Network.Device)
			}
		}
	}
	boot.Cmdline = cmdline.String()

	return sc, nil
}

// prepareCloudimg creates a qcow2 COW overlay backed by the base image blob.
// Returns the updated StorageConfig slice (replaced with the overlay).
func (ch *CloudHypervisor) prepareCloudimg(ctx context.Context, vmID string, vmCfg *types.VMConfig, sc []*types.StorageConfig, nc []*types.NetworkConfig) ([]*types.StorageConfig, error) {
	if len(sc) == 0 {
		return nil, fmt.Errorf("cloudimg: no base image StorageConfig")
	}
	basePath := sc[0].Path
	overlayPath := ch.conf.CHVMOverlayPath(vmID)

	// qemu-img create -f qcow2 -F qcow2 -b <base> <overlay>
	if out, err := exec.CommandContext(ctx, //nolint:gosec
		"qemu-img", "create", "-f", "qcow2", "-F", "qcow2",
		"-b", basePath, overlayPath,
	).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("qemu-img create overlay: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// qemu-img resize <overlay> <size>
	if vmCfg.Storage > 0 {
		sizeBytes := fmt.Sprintf("%d", vmCfg.Storage)
		if out, err := exec.CommandContext(ctx, //nolint:gosec
			"qemu-img", "resize", overlayPath, sizeBytes,
		).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("qemu-img resize overlay: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	// Generate cloud-init cidata disk.
	metaCfg := &metadata.Config{
		InstanceID:   vmID,
		Hostname:     vmCfg.Name,
		RootPassword: ch.conf.DefaultRootPassword,
	}
	for _, n := range nc {
		if n.Network != nil {
			ones, _ := n.Network.Netmask.Size()
			metaCfg.Networks = append(metaCfg.Networks, metadata.NetworkInfo{
				IP:      n.Network.IP.String(),
				Prefix:  ones,
				Gateway: n.Network.Gateway.String(),
				Device:  n.Network.Device,
				Mac:     n.Mac,
			})
		}
	}

	cidataPath := ch.conf.CHVMCidataPath(vmID)
	f, err := os.Create(cidataPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("create cidata: %w", err)
	}
	if err := metadata.Generate(f, metaCfg); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("generate cidata: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close cidata: %w", err)
	}

	// Replace StorageConfigs with the overlay + cidata (base is accessed via backing file chain).
	return []*types.StorageConfig{
		{Path: overlayPath, RO: false},
		{Path: cidataPath, RO: true},
	}, nil
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
