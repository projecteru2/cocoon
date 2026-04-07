package firecracker

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// CowSerial is the well-known virtio serial for the COW disk.
// FC doesn't expose serial to the guest, but this is kept in the DB record
// for consistency with CH and snapshot/clone operations.
const CowSerial = "cocoon-cow"

// Create registers a new VM, prepares the COW disk, and persists the record.
// The VM is left in Created state — call Start to launch it.
// FC only supports OCI images (direct kernel boot).
func (fc *Firecracker) Create(ctx context.Context, id string, vmCfg *types.VMConfig, storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig, bootCfg *types.BootConfig) (_ *types.VM, err error) {
	now := time.Now()
	runDir := fc.conf.VMRunDir(id)
	logDir := fc.conf.VMLogDir(id)

	blobIDs := extractBlobIDs(storageConfigs, bootCfg)

	defer func() {
		if err != nil {
			_ = removeVMDirs(runDir, logDir)
			fc.rollbackCreate(ctx, id, vmCfg.Name)
		}
	}()

	if err = fc.reserveVM(ctx, id, vmCfg, blobIDs, runDir, logDir); err != nil {
		return nil, fmt.Errorf("reserve VM record: %w", err)
	}
	if err = utils.EnsureDirs(runDir, logDir); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}

	var bootCopy *types.BootConfig
	if bootCfg != nil {
		b := *bootCfg
		bootCopy = &b
	}

	preparedStorage, err := fc.prepareOCI(ctx, id, vmCfg, storageConfigs, networkConfigs, bootCopy)
	if err != nil {
		return nil, err
	}

	info := types.VM{
		ID: id, State: types.VMStateCreated,
		Config:         *vmCfg,
		StorageConfigs: preparedStorage,
		NetworkConfigs: networkConfigs,
		CreatedAt:      now, UpdatedAt: now,
	}
	rec := hypervisor.VMRecord{
		VM:           info,
		BootConfig:   bootCopy,
		ImageBlobIDs: blobIDs,
		RunDir:       runDir,
		LogDir:       logDir,
	}
	if err := fc.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		idx.VMs[id] = &rec
		return nil
	}); err != nil {
		return nil, fmt.Errorf("finalize VM record: %w", err)
	}
	return &info, nil
}

// prepareOCI creates a raw COW disk, appends the COW StorageConfig, and builds
// the kernel cmdline with device-path mappings (FC lacks virtio serial support).
func (fc *Firecracker) prepareOCI(ctx context.Context, vmID string, vmCfg *types.VMConfig, storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig, boot *types.BootConfig) ([]*types.StorageConfig, error) {
	cowPath := fc.conf.COWRawPath(vmID)

	f, err := os.OpenFile(cowPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("create COW: %w", err)
	}
	_ = f.Close()
	if err = os.Truncate(cowPath, vmCfg.Storage); err != nil {
		return nil, fmt.Errorf("truncate COW: %w", err)
	}
	out, err := exec.CommandContext(ctx, //nolint:gosec
		"mkfs.ext4", "-F", "-m", "0", "-q",
		"-E", "lazy_itable_init=1,lazy_journal_init=1,discard",
		cowPath,
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("mkfs.ext4 COW: %s: %w", strings.TrimSpace(string(out)), err)
	}

	storageConfigs = append(storageConfigs, &types.StorageConfig{
		Path:   cowPath,
		RO:     false,
		Serial: CowSerial,
	})

	dns, err := fc.conf.DNSServers()
	if err != nil {
		return nil, fmt.Errorf("parse DNS servers: %w", err)
	}
	boot.Cmdline = buildCmdline(storageConfigs, networkConfigs, vmCfg.Name, dns)
	return storageConfigs, nil
}

// buildCmdline generates the kernel cmdline for FC VMs.
// FC doesn't support virtio serial, so disks are referenced by device path
// (/dev/vda, /dev/vdb, ...) based on the order drives are attached.
func buildCmdline(storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig, vmName string, dnsServers []string) string {
	nLayers := 0
	for _, s := range storageConfigs {
		if s.RO {
			nLayers++
		}
	}

	// Build layer device paths reversed (top layer first for overlayfs lowerdir)
	layerDevs := make([]string, nLayers)
	for i := range nLayers {
		layerDevs[nLayers-1-i] = devPath(i)
	}
	cowDev := devPath(nLayers)

	var cmdline strings.Builder
	// FC serial console is ttyS0 (not hvc0 like CH's virtio-console)
	fmt.Fprintf(&cmdline,
		"console=ttyS0 loglevel=3 boot=cocoon-overlay cocoon.layers=%s cocoon.cow=%s clocksource=kvm-clock rw",
		strings.Join(layerDevs, ","), cowDev,
	)

	if len(networkConfigs) > 0 {
		cmdline.WriteString(" net.ifnames=0")
		cmdline.WriteString(buildIPParams(networkConfigs, vmName, dnsServers))
	}

	return cmdline.String()
}

// devPath returns the virtio block device path for the i-th drive.
func devPath(idx int) string {
	return fmt.Sprintf("/dev/vd%c", 'a'+idx)
}

// buildIPParams generates kernel ip= parameters for NICs with static IPs.
func buildIPParams(networkConfigs []*types.NetworkConfig, vmName string, dnsServers []string) string {
	var params strings.Builder
	fmt.Fprintf(&params, " cocoon.hostname=%s", vmName)
	var dns0, dns1 string
	if len(dnsServers) > 0 {
		dns0 = dnsServers[0]
	}
	if len(dnsServers) > 1 {
		dns1 = dnsServers[1]
	}
	for i, n := range networkConfigs {
		if n.Network == nil || n.Network.IP == "" {
			continue
		}
		param := fmt.Sprintf(" ip=%s::%s:%s:%s:eth%d:off",
			n.Network.IP, n.Network.Gateway,
			prefixToNetmask(n.Network.Prefix), vmName, i)
		if dns0 != "" {
			param += ":" + dns0
			if dns1 != "" {
				param += ":" + dns1
			}
		}
		params.WriteString(param)
	}
	return params.String()
}

func prefixToNetmask(prefix int) string {
	mask := net.CIDRMask(prefix, 32)
	return net.IP(mask).String()
}

// extractBlobIDs extracts digest hexes from storage/boot paths for GC pinning.
func extractBlobIDs(storageConfigs []*types.StorageConfig, boot *types.BootConfig) map[string]struct{} {
	ids := make(map[string]struct{})
	if boot != nil && boot.KernelPath != "" {
		for _, s := range storageConfigs {
			if s.RO {
				ids[blobHexFromPath(s.Path)] = struct{}{}
			}
		}
		ids[filepath.Base(filepath.Dir(boot.KernelPath))] = struct{}{}
		if boot.InitrdPath != "" {
			ids[filepath.Base(filepath.Dir(boot.InitrdPath))] = struct{}{}
		}
	}
	return ids
}

func blobHexFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
