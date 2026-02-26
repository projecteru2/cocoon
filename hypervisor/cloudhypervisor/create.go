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
func (ch *CloudHypervisor) Create(ctx context.Context, id string, vmCfg *types.VMConfig, storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig, bootCfg *types.BootConfig) (*types.VM, error) { //nolint:cyclop
	var err error
	now := time.Now()

	blobIDs := extractBlobIDs(storageConfigs, bootCfg)

	// Step 1: write a placeholder record so GC won't treat our dirs as orphans.
	if updateErr := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
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
	}); updateErr != nil {
		return nil, fmt.Errorf("reserve VM record: %w", updateErr)
	}

	// Step 2: create directories and prepare disks.
	if dirErr := ch.conf.EnsureCHVMDirs(id); dirErr != nil {
		ch.rollbackCreate(ctx, id, vmCfg.Name)
		return nil, fmt.Errorf("ensure dirs: %w", dirErr)
	}

	var (
		preparedStorage []*types.StorageConfig
		bootCopy        *types.BootConfig
	)
	if bootCfg != nil {
		b := *bootCfg
		bootCopy = &b
	}
	if bootCopy != nil && bootCopy.KernelPath != "" {
		preparedStorage, err = ch.prepareOCI(ctx, id, vmCfg, storageConfigs, networkConfigs, bootCopy)
	} else {
		preparedStorage, err = ch.prepareCloudimg(ctx, id, vmCfg, storageConfigs, networkConfigs)
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
		StorageConfigs: preparedStorage,
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
func (ch *CloudHypervisor) prepareOCI(ctx context.Context, vmID string, vmCfg *types.VMConfig, storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig, boot *types.BootConfig) ([]*types.StorageConfig, error) {
	cowPath := ch.conf.CHVMCOWRawPath(vmID)

	// Create sparse COW file (equivalent to truncate -s <size>).
	// os.Truncate requires the file to exist; create it first.
	f, err := os.Create(cowPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("create COW: %w", err)
	}
	if err := f.Truncate(vmCfg.Storage); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("truncate COW: %w", err)
	}
	_ = f.Close()
	// mkfs.ext4
	if out, err := exec.CommandContext(ctx, //nolint:gosec
		"mkfs.ext4", "-F", "-m", "0", "-q",
		"-E", "lazy_itable_init=1,lazy_journal_init=1,discard",
		cowPath,
	).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mkfs.ext4 COW: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Append COW StorageConfig.
	storageConfigs = append(storageConfigs, &types.StorageConfig{
		Path:   cowPath,
		RO:     false,
		Serial: CowSerial,
	})

	// Build cmdline with reversed layer serials for overlayfs lowerdir ordering (top layer first).
	var cmdline strings.Builder
	fmt.Fprintf(&cmdline,
		"console=hvc0 loglevel=3 boot=cocoon-overlay cocoon.layers=%s cocoon.cow=%s clocksource=kvm-clock rw",
		strings.Join(ReverseLayerSerials(storageConfigs), ","), CowSerial,
	)

	// Append static IP configuration for each network interface.
	// Format: ip=<client-IP>:<server>:<gw-IP>:<netmask>:<hostname>:<device>:<autoconf>
	// The index i matches the CH --net ordering, which maps 1:1 to guest eth{i}.
	// NICs with Network==nil (DHCP) still occupy their slot but get no ip= param.
	if len(networkConfigs) > 0 {
		cmdline.WriteString(" net.ifnames=0")
		for i, n := range networkConfigs {
			if n.Network != nil && n.Network.IP != nil {
				gw := ""
				if n.Network.Gateway != nil {
					gw = n.Network.Gateway.String()
				}
				fmt.Fprintf(&cmdline, " ip=%s::%s:%s:%s:eth%d:off",
					n.Network.IP, gw,
					net.IP(n.Network.Netmask), vmCfg.Name, i)
			}
		}
	}
	boot.Cmdline = cmdline.String()

	return storageConfigs, nil
}

// prepareCloudimg creates a qcow2 COW overlay backed by the base image blob.
// Returns the updated StorageConfig slice (replaced with the overlay).
func (ch *CloudHypervisor) prepareCloudimg(ctx context.Context, vmID string, vmCfg *types.VMConfig, storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig) ([]*types.StorageConfig, error) {
	if len(storageConfigs) == 0 {
		return nil, fmt.Errorf("cloudimg: no base image StorageConfig")
	}
	basePath := storageConfigs[0].Path
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
	// Index i matches CH --net order → guest eth{i}. See prepareOCI comment.
	for i, n := range networkConfigs {
		if n.Network != nil && n.Network.IP != nil {
			ones, _ := n.Network.Netmask.Size()
			ni := metadata.NetworkInfo{
				IP:     n.Network.IP.String(),
				Prefix: ones,
				Device: fmt.Sprintf("eth%d", i),
				Mac:    n.Mac,
			}
			if n.Network.Gateway != nil {
				ni.Gateway = n.Network.Gateway.String()
			}
			metaCfg.Networks = append(metaCfg.Networks, ni)
		}
	}

	cidataPath := ch.conf.CHVMCidataPath(vmID)
	f, err := os.OpenFile(cidataPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec
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
func extractBlobIDs(storageConfigs []*types.StorageConfig, boot *types.BootConfig) map[string]struct{} {
	ids := make(map[string]struct{})
	if boot != nil && boot.KernelPath != "" {
		// OCI: erofs layer blobs + boot dir hexes.
		for _, s := range storageConfigs {
			if s.RO {
				ids[blobHexFromPath(s.Path)] = struct{}{}
			}
		}
		// boot/{hex}/vmlinuz → hex
		ids[filepath.Base(filepath.Dir(boot.KernelPath))] = struct{}{}
		if boot.InitrdPath != "" {
			ids[filepath.Base(filepath.Dir(boot.InitrdPath))] = struct{}{}
		}
	} else if len(storageConfigs) > 0 {
		// Cloudimg: base qcow2 blob hex (before overlay replaces it).
		ids[blobHexFromPath(storageConfigs[0].Path)] = struct{}{}
	}
	return ids
}
