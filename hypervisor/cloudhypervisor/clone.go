package cloudhypervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// Clone creates a new VM from a snapshot tar stream via vm.restore.
// Three phases: placeholder record → extract+prepare → launch+finalize.
func (ch *CloudHypervisor) Clone(ctx context.Context, vmID string, vmCfg *types.VMConfig, snapshotCfg *types.SnapshotConfig, networkConfigs []*types.NetworkConfig, snapshot io.Reader) (*types.VM, error) {
	logger := log.WithFunc("cloudhypervisor.Clone")

	if vmCfg.Image == "" && snapshotCfg.Image != "" {
		vmCfg.Image = snapshotCfg.Image
	}

	now := time.Now()
	runDir := ch.conf.VMRunDir(vmID)
	logDir := ch.conf.VMLogDir(vmID)

	success := false
	defer func() {
		if !success {
			_ = removeVMDirs(runDir, logDir)
			ch.rollbackCreate(ctx, vmID, vmCfg.Name)
		}
	}()

	// Phase 1: placeholder record so GC won't orphan dirs.
	if err := ch.reserveVM(ctx, vmID, vmCfg, snapshotCfg.ImageBlobIDs, runDir, logDir); err != nil {
		return nil, fmt.Errorf("reserve VM record: %w", err)
	}

	// Phase 2: extract + prepare.
	if err := utils.EnsureDirs(runDir, logDir); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}
	if err := utils.ExtractTar(runDir, snapshot); err != nil {
		return nil, fmt.Errorf("extract snapshot: %w", err)
	}

	chConfigPath := filepath.Join(runDir, "config.json")
	chCfg, err := parseCHConfig(chConfigPath)
	if err != nil {
		return nil, fmt.Errorf("parse CH config: %w", err)
	}

	storageConfigs := rebuildStorageConfigs(chCfg)
	bootCfg := rebuildBootConfig(chCfg)
	blobIDs := extractBlobIDs(storageConfigs, bootCfg)
	directBoot := isDirectBoot(bootCfg)

	var cowPath string
	if directBoot {
		cowPath = ch.conf.COWRawPath(vmID)
	} else {
		cowPath = ch.conf.OverlayPath(vmID)
	}
	if err = updateCOWPath(storageConfigs, cowPath, directBoot); err != nil {
		return nil, fmt.Errorf("update COW path: %w", err)
	}

	// Update cidata path (cloudimg only; may be absent if snapshot was taken after restart).
	hadCidataInSnapshot := updateCloneCidataPath(storageConfigs, directBoot, ch.conf.CidataPath(vmID))

	if err = verifyBaseFiles(storageConfigs, bootCfg); err != nil {
		return nil, fmt.Errorf("verify base files: %w", err)
	}
	if vmCfg.Storage > 0 {
		if err = resizeCOW(ctx, cowPath, vmCfg.Storage, directBoot); err != nil {
			return nil, fmt.Errorf("resize COW: %w", err)
		}
	}

	stateReplacements := buildStateReplacements(chCfg, storageConfigs, networkConfigs)

	// Cloudimg: regenerate cidata with clone's identity and network config.
	storageConfigs, err = ch.ensureCloneCidata(vmID, vmCfg, networkConfigs, storageConfigs, directBoot)
	if err != nil {
		return nil, err
	}

	// vm.restore requires config/state device tree equality.
	// If snapshot had no cidata disk, patch only snapshot disks and hotplug cidata later.
	patchStorageConfigs := restorePatchStorageConfigs(storageConfigs, directBoot, hadCidataInSnapshot)

	consoleSock := filepath.Join(runDir, "console.sock")
	if err = patchCHConfig(chConfigPath, &patchOptions{
		storageConfigs: patchStorageConfigs,
		networkConfigs: networkConfigs,
		consoleSock:    consoleSock,
		directBoot:     directBoot,
		cpu:            vmCfg.CPU,
		memory:         vmCfg.Memory,
		vmName:         vmCfg.Name,
		dnsServers:     ch.conf.DNSServers(),
	}); err != nil {
		return nil, fmt.Errorf("patch CH config: %w", err)
	}

	// Patch state.json: disk paths (informational) + MAC addresses (functional).
	stateJSONPath := filepath.Join(runDir, "state.json")
	if err = patchStateJSON(stateJSONPath, stateReplacements); err != nil {
		return nil, fmt.Errorf("patch state.json: %w", err)
	}

	// Update bootCfg.Cmdline for restarts (new VM name, IP, DNS).
	if directBoot && bootCfg != nil {
		bootCfg.Cmdline = buildCmdline(storageConfigs, networkConfigs, vmCfg.Name, ch.conf.DNSServers())
	}

	// Phase 3: launch CH, restore, finalize.
	sockPath := socketPath(runDir)
	args := []string{"--api-socket", sockPath}
	ch.saveCmdline(ctx, &hypervisor.VMRecord{RunDir: runDir}, args)

	withNetwork := len(networkConfigs) > 0
	pid, err := ch.launchProcess(ctx, &hypervisor.VMRecord{
		VM:     types.VM{NetworkConfigs: networkConfigs},
		RunDir: runDir,
		LogDir: logDir,
	}, sockPath, args, withNetwork)
	if err != nil {
		ch.markError(ctx, vmID)
		return nil, fmt.Errorf("launch CH: %w", err)
	}

	if err := ch.restoreAndResumeClone(ctx, pid, sockPath, runDir, directBoot, hadCidataInSnapshot, storageConfigs, vmCfg.CPU); err != nil {
		return nil, err
	}

	// Finalize record → Running.
	info := types.VM{
		ID:             vmID,
		State:          types.VMStateRunning,
		Config:         *vmCfg,
		StorageConfigs: storageConfigs,
		NetworkConfigs: networkConfigs,
		CreatedAt:      now,
		UpdatedAt:      now,
		StartedAt:      &now,
	}
	if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r := idx.VMs[vmID]
		if r == nil {
			return fmt.Errorf("VM %s disappeared from index", vmID)
		}
		r.VM = info
		r.BootConfig = bootCfg
		r.ImageBlobIDs = blobIDs
		// Cloudimg: FirstBooted=false → first restart attaches cidata → cloud-init re-runs.
		r.FirstBooted = directBoot
		return nil
	}); err != nil {
		ch.abortLaunch(ctx, pid, sockPath, runDir)
		return nil, fmt.Errorf("finalize VM record: %w", err)
	}

	success = true
	logger.Infof(ctx, "VM %s cloned from snapshot", vmID)
	return &info, nil
}

func (ch *CloudHypervisor) restoreAndResumeClone(
	ctx context.Context,
	pid int,
	sockPath, runDir string,
	directBoot, hadCidataInSnapshot bool,
	storageConfigs []*types.StorageConfig,
	cpu int,
) error {
	hc := utils.NewSocketHTTPClient(sockPath)
	if err := restoreVM(ctx, hc, runDir); err != nil {
		ch.abortLaunch(ctx, pid, sockPath, runDir)
		return fmt.Errorf("vm.restore: %w", err)
	}
	if !directBoot && !hadCidataInSnapshot {
		if len(storageConfigs) == 0 {
			ch.abortLaunch(ctx, pid, sockPath, runDir)
			return fmt.Errorf("vm.add-disk (cidata): missing storage config")
		}
		cidataDisk := storageConfigToDisk(storageConfigs[len(storageConfigs)-1], cpu)
		if err := addDiskVM(ctx, hc, cidataDisk); err != nil {
			ch.abortLaunch(ctx, pid, sockPath, runDir)
			return fmt.Errorf("vm.add-disk (cidata): %w", err)
		}
	}
	if err := resumeVM(ctx, hc); err != nil {
		ch.abortLaunch(ctx, pid, sockPath, runDir)
		return fmt.Errorf("vm.resume: %w", err)
	}
	return nil
}

func parseCHConfig(path string) (*chVMConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg chVMConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &cfg, nil
}

func rebuildStorageConfigs(cfg *chVMConfig) []*types.StorageConfig {
	var configs []*types.StorageConfig
	for _, d := range cfg.Disks {
		configs = append(configs, &types.StorageConfig{
			Path:   d.Path,
			RO:     d.ReadOnly,
			Serial: d.Serial,
		})
	}
	return configs
}

func rebuildBootConfig(cfg *chVMConfig) *types.BootConfig {
	if cfg.Payload == nil {
		return nil
	}
	p := cfg.Payload
	if p.Kernel == "" && p.Firmware == "" {
		return nil
	}
	return &types.BootConfig{
		KernelPath:   p.Kernel,
		InitrdPath:   p.Initramfs,
		Cmdline:      p.Cmdline,
		FirmwarePath: p.Firmware,
	}
}

func updateCloneCidataPath(storageConfigs []*types.StorageConfig, directBoot bool, cidataPath string) bool {
	if directBoot {
		return false
	}
	hadCidataInSnapshot := false
	for _, sc := range storageConfigs {
		if isCidataDisk(sc) {
			sc.Path = cidataPath
			hadCidataInSnapshot = true
		}
	}
	return hadCidataInSnapshot
}

func (ch *CloudHypervisor) ensureCloneCidata(vmID string, vmCfg *types.VMConfig, networkConfigs []*types.NetworkConfig, storageConfigs []*types.StorageConfig, directBoot bool) ([]*types.StorageConfig, error) {
	if directBoot {
		return storageConfigs, nil
	}
	if err := ch.generateCidata(vmID, vmCfg, networkConfigs); err != nil {
		return nil, fmt.Errorf("generate cidata: %w", err)
	}
	cidataPath := ch.conf.CidataPath(vmID)
	// Keep cidata in VM record for future starts; snapshot may not carry it.
	if !slices.ContainsFunc(storageConfigs, isCidataDisk) {
		storageConfigs = append(storageConfigs, &types.StorageConfig{
			Path: cidataPath,
			RO:   true,
		})
	}
	return storageConfigs, nil
}

func restorePatchStorageConfigs(storageConfigs []*types.StorageConfig, directBoot, hadCidataInSnapshot bool) []*types.StorageConfig {
	if directBoot || hadCidataInSnapshot || len(storageConfigs) == 0 {
		return storageConfigs
	}
	return storageConfigs[:len(storageConfigs)-1]
}

func verifyBaseFiles(storageConfigs []*types.StorageConfig, boot *types.BootConfig) error {
	for _, sc := range storageConfigs {
		if !sc.RO {
			continue
		}
		if _, err := os.Stat(sc.Path); err != nil {
			return fmt.Errorf("base layer %s: %w", sc.Path, err)
		}
	}
	if boot == nil {
		return nil
	}
	if boot.KernelPath != "" {
		if _, err := os.Stat(boot.KernelPath); err != nil {
			return fmt.Errorf("kernel %s: %w", boot.KernelPath, err)
		}
	}
	if boot.InitrdPath != "" {
		if _, err := os.Stat(boot.InitrdPath); err != nil {
			return fmt.Errorf("initrd %s: %w", boot.InitrdPath, err)
		}
	}
	if boot.FirmwarePath != "" {
		if _, err := os.Stat(boot.FirmwarePath); err != nil {
			return fmt.Errorf("firmware %s: %w", boot.FirmwarePath, err)
		}
	}
	return nil
}

func updateCOWPath(configs []*types.StorageConfig, newCOWPath string, directBoot bool) error {
	if directBoot {
		found := false
		for _, sc := range configs {
			if !sc.RO && sc.Serial == CowSerial {
				sc.Path = newCOWPath
				found = true
			}
		}
		if !found {
			return fmt.Errorf("no writable disk with serial %q found", CowSerial)
		}
		return nil
	}
	for _, sc := range configs {
		if !sc.RO {
			sc.Path = newCOWPath
		}
	}
	return nil
}

func resizeCOW(ctx context.Context, cowPath string, targetSize int64, directBoot bool) error {
	fi, err := os.Stat(cowPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", cowPath, err)
	}
	if targetSize <= fi.Size() {
		return nil // already large enough
	}

	if directBoot {
		if err := os.Truncate(cowPath, targetSize); err != nil {
			return fmt.Errorf("truncate %s to %d: %w", cowPath, targetSize, err)
		}
	} else {
		sizeStr := fmt.Sprintf("%d", targetSize)
		if out, err := exec.CommandContext(ctx, //nolint:gosec
			"qemu-img", "resize", cowPath, sizeStr,
		).CombinedOutput(); err != nil {
			return fmt.Errorf("qemu-img resize %s: %s: %w", cowPath, strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}

func buildCmdline(storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig, vmName string, dnsServers []string) string {
	var cmdline strings.Builder
	fmt.Fprintf(&cmdline,
		"console=hvc0 loglevel=3 boot=cocoon-overlay cocoon.layers=%s cocoon.cow=%s clocksource=kvm-clock rw",
		strings.Join(ReverseLayerSerials(storageConfigs), ","), CowSerial,
	)

	if len(networkConfigs) > 0 {
		cmdline.WriteString(" net.ifnames=0")
		dns0, dns1 := dnsFromConfig(dnsServers)
		for i, n := range networkConfigs {
			if n.Network == nil || n.Network.IP == "" {
				continue
			}
			param := fmt.Sprintf("ip=%s::%s:%s:%s:eth%d:off",
				n.Network.IP, n.Network.Gateway,
				prefixToNetmask(n.Network.Prefix), vmName, i)
			if dns0 != "" {
				param += ":" + dns0
				if dns1 != "" {
					param += ":" + dns1
				}
			}
			cmdline.WriteString(" " + param)
		}
	}

	return cmdline.String()
}

// buildStateReplacements builds old→new string mappings for state.json patching.
// Includes disk paths (informational) and MAC addresses (functional — the guest
// virtio-net device state has the MAC baked in; without patching, the guest NIC
// keeps the snapshot's MAC, breaking CNI anti-spoofing and cidata MAC matching).
//
// MAC addresses in state.json are serialized by serde as decimal byte arrays
// (e.g. "4e:08:ba:c1:62:f8" → "78,8,186,193,98,248"), so we convert both old
// and new MACs to that format for string replacement.
func buildStateReplacements(chCfg *chVMConfig, storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig) map[string]string {
	m := make(map[string]string, len(chCfg.Disks)+len(chCfg.Nets))
	if len(storageConfigs) == len(chCfg.Disks) {
		for i, d := range chCfg.Disks {
			if storageConfigs[i].Path != d.Path {
				m[d.Path] = storageConfigs[i].Path
			}
		}
	}
	for i, n := range chCfg.Nets {
		if i >= len(networkConfigs) {
			break
		}
		if n.Mac != "" && networkConfigs[i].Mac != "" && n.Mac != networkConfigs[i].Mac {
			oldBytes, err1 := macToSerdeBytes(n.Mac)
			newBytes, err2 := macToSerdeBytes(networkConfigs[i].Mac)
			if err1 == nil && err2 == nil {
				m[oldBytes] = newBytes
			}
		}
	}
	return m
}

// macToSerdeBytes converts a colon-separated MAC like "4e:08:ba:c1:62:f8" to
// the serde JSON byte-array form "78,8,186,193,98,248" used in CH's state.json.
func macToSerdeBytes(mac string) (string, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return "", fmt.Errorf("parse MAC %q: %w", mac, err)
	}
	parts := make([]string, len(hw))
	for i, b := range hw {
		parts[i] = fmt.Sprintf("%d", b)
	}
	return strings.Join(parts, ","), nil
}
