package firecracker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

const cloneBackupSuffix = ".cocoon-clone-backup"

type driveRedirect struct {
	symlinkPath string
	backupPath  string
	createdDir  bool
}

func (fc *Firecracker) Clone(ctx context.Context, vmID string, vmCfg *types.VMConfig, networkConfigs []*types.NetworkConfig, snapshotConfig *types.SnapshotConfig, snapshot io.Reader) (*types.VM, error) {
	return fc.CloneFromStream(ctx, vmID, vmCfg, networkConfigs, snapshotConfig, snapshot, fc.cloneAfterExtract)
}

func (fc *Firecracker) cloneAfterExtract(ctx context.Context, vmID string, vmCfg *types.VMConfig, networkConfigs []*types.NetworkConfig, runDir, logDir string, now time.Time) (*types.VM, error) {
	logger := log.WithFunc("firecracker.Clone")

	meta, err := hypervisor.LoadAndValidateMeta(runDir, fc.conf.RootDir, fc.conf.Config.RunDir)
	if err != nil {
		return nil, fmt.Errorf("load snapshot metadata: %w", err)
	}

	// FC cannot update CPU/memory after snapshot/load.
	if meta.CPU > 0 && vmCfg.CPU != meta.CPU {
		return nil, fmt.Errorf("--cpu %d not supported: Firecracker cannot change CPU after snapshot/load (snapshot has %d)", vmCfg.CPU, meta.CPU)
	}
	if meta.Memory > 0 && vmCfg.Memory != meta.Memory {
		return nil, fmt.Errorf("--memory not supported: Firecracker cannot change memory after snapshot/load")
	}

	cowPath := fc.conf.COWRawPath(vmID)
	snapshotCOW := filepath.Join(runDir, cowFileName)
	if renameErr := os.Rename(snapshotCOW, cowPath); renameErr != nil {
		return nil, fmt.Errorf("move COW to canonical path: %w", renameErr)
	}

	storageConfigs, err := rebuildCloneStorage(meta, cowPath)
	if err != nil {
		return nil, err
	}
	if err := types.ValidateStorageConfigs(storageConfigs); err != nil {
		return nil, fmt.Errorf("validate sidecar: %w", err)
	}
	bootCfg := meta.BootConfig
	if bootCfg != nil && bootCfg.KernelPath != "" {
		vmlinuxPath, extractErr := EnsureVmlinux(bootCfg.KernelPath)
		if extractErr != nil {
			return nil, fmt.Errorf("extract vmlinux for clone: %w", extractErr)
		}
		bootCfg.KernelPath = vmlinuxPath
	}
	blobIDs := hypervisor.ExtractBlobIDs(storageConfigs, bootCfg)

	if verifyErr := hypervisor.VerifyBaseFiles(storageConfigs, bootCfg); verifyErr != nil {
		return nil, fmt.Errorf("verify base files: %w", verifyErr)
	}
	if vmCfg.Storage > 0 {
		if expandErr := hypervisor.ExpandRawImage(cowPath, vmCfg.Storage); expandErr != nil {
			return nil, fmt.Errorf("resize COW: %w", expandErr)
		}
	}
	if bootCfg != nil {
		dns, dnsErr := fc.conf.DNSServers()
		if dnsErr != nil {
			return nil, fmt.Errorf("parse DNS servers: %w", dnsErr)
		}
		bootCfg.Cmdline = buildCmdline(storageConfigs, networkConfigs, vmCfg.Name, dns)
	}

	// FC snapshot/load requires drives at source absolute paths; only COW path
	// changed, so symlink-redirect the source path until upstream supports
	// drive overrides at load time.
	sockPath := hypervisor.SocketPath(runDir)
	withNetwork := len(networkConfigs) > 0
	var pid int
	if cloneErr := withSourceWritableDisksLocked(meta.StorageConfigs, func() error {
		redirects, redirectErr := createDriveRedirects(meta.StorageConfigs, storageConfigs)
		if redirectErr != nil {
			return fmt.Errorf("drive redirect: %w", redirectErr)
		}
		defer cleanupDriveRedirects(redirects)

		var launchErr error
		pid, launchErr = fc.launchProcess(ctx, &hypervisor.VMRecord{
			VM:     types.VM{ID: vmID, NetworkConfigs: networkConfigs},
			RunDir: runDir,
			LogDir: logDir,
		}, sockPath, withNetwork)
		if launchErr != nil {
			return fmt.Errorf("launch FC: %w", launchErr)
		}

		return fc.restoreAndResumeClone(ctx, pid, sockPath, runDir, networkConfigs)
	}); cloneErr != nil {
		fc.MarkError(ctx, vmID)
		return nil, cloneErr
	}

	info := &types.VM{
		ID: vmID, Hypervisor: typ, State: types.VMStateRunning,
		Config: *vmCfg, StorageConfigs: storageConfigs, NetworkConfigs: networkConfigs,
		CreatedAt: now, UpdatedAt: now, StartedAt: &now,
	}
	if err := fc.FinalizeClone(ctx, vmID, info, bootCfg, blobIDs); err != nil {
		fc.AbortLaunch(ctx, pid, sockPath, runDir, runtimeFiles)
		return nil, fmt.Errorf("finalize VM record: %w", err)
	}

	logger.Infof(ctx, "VM %s cloned from snapshot", vmID)
	return info, nil
}

func (fc *Firecracker) restoreAndResumeClone(
	ctx context.Context,
	pid int,
	sockPath, runDir string,
	networkConfigs []*types.NetworkConfig,
) (err error) {
	defer func() {
		if err != nil {
			fc.AbortLaunch(ctx, pid, sockPath, runDir, runtimeFiles)
		}
	}()

	// network_overrides points FC at the clone's TAP instead of the source TAP
	// (FC recreates devices from vmstate). The earlier symlink redirect makes
	// FC open the clone's COW; held fds survive its cleanup.
	netOverrides := buildNetworkOverrides(networkConfigs)
	if err = loadSnapshotFC(ctx, sockPath, runDir, netOverrides); err != nil {
		return fmt.Errorf("snapshot/load: %w", err)
	}
	hc := utils.NewSocketHTTPClient(sockPath)
	if err = resumeVM(ctx, hc); err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	return nil
}

// rebuildCloneStorage rewrites paths per role: Layer keeps source path (shared
// blob), COW → clone cowPath, Data → clone runDir. Cidata is rejected (FC has
// no cloudimg path).
func rebuildCloneStorage(meta *hypervisor.SnapshotMeta, cowPath string) ([]*types.StorageConfig, error) {
	runDir := filepath.Dir(cowPath)
	configs := hypervisor.CloneStorageConfigs(meta.StorageConfigs)
	for i, sc := range configs {
		switch sc.Role {
		case types.StorageRoleLayer:
		case types.StorageRoleCOW:
			sc.Path = cowPath
		case types.StorageRoleData:
			sc.Path = filepath.Join(runDir, hypervisor.DataDiskBaseName(sc.Serial))
		case types.StorageRoleCidata:
			return nil, fmt.Errorf("snapshot disk[%d] has cidata role; FC does not support cloudimg", i)
		default:
			return nil, fmt.Errorf("snapshot disk[%d] has unknown role %q", i, sc.Role)
		}
	}
	return configs, nil
}

// createDriveRedirects creates temporary symlinks from source COW path to
// clone COW path so FC snapshot/load can find drives at expected locations.
// Only creates redirects for paths that actually differ (i.e., COW disk).
func createDriveRedirects(srcConfigs, dstConfigs []*types.StorageConfig) ([]driveRedirect, error) {
	var redirects []driveRedirect
	for i, src := range srcConfigs {
		if i >= len(dstConfigs) || src.Path == dstConfigs[i].Path {
			continue
		}
		r := driveRedirect{symlinkPath: src.Path}

		if _, err := os.Stat(src.Path); err == nil {
			backup := src.Path + cloneBackupSuffix
			if renameErr := os.Rename(src.Path, backup); renameErr != nil {
				cleanupDriveRedirects(redirects)
				return nil, fmt.Errorf("backup source drive %s: %w", src.Path, renameErr)
			}
			r.backupPath = backup
		}

		if _, err := os.Stat(filepath.Dir(src.Path)); err != nil {
			if mkErr := os.MkdirAll(filepath.Dir(src.Path), 0o700); mkErr != nil {
				cleanupDriveRedirects(redirects)
				return nil, fmt.Errorf("create dir for drive redirect %s: %w", src.Path, mkErr)
			}
			r.createdDir = true
		}

		if linkErr := os.Symlink(dstConfigs[i].Path, src.Path); linkErr != nil {
			if r.backupPath != "" {
				_ = os.Rename(r.backupPath, src.Path)
			}
			cleanupDriveRedirects(redirects)
			return nil, fmt.Errorf("symlink drive redirect %s → %s: %w", src.Path, dstConfigs[i].Path, linkErr)
		}
		redirects = append(redirects, r)
	}
	return redirects, nil
}

func cleanupDriveRedirects(redirects []driveRedirect) {
	for _, r := range redirects {
		_ = os.Remove(r.symlinkPath)
		if r.backupPath != "" {
			_ = os.Rename(r.backupPath, r.symlinkPath)
		}
		if r.createdDir {
			_ = os.Remove(filepath.Dir(r.symlinkPath))
		}
	}
}

// recoverStaleBackup restores a backup file left by a crashed clone.
// Caller must hold the COW lock.
func recoverStaleBackup(cowPath string) {
	backup := cowPath + cloneBackupSuffix
	if _, err := os.Stat(backup); err != nil {
		return
	}
	fi, err := os.Lstat(cowPath)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(cowPath)
	}
	_ = os.Rename(backup, cowPath)
}

func buildNetworkOverrides(networkConfigs []*types.NetworkConfig) []fcNetworkOverride {
	var overrides []fcNetworkOverride
	for i, nc := range networkConfigs {
		overrides = append(overrides, fcNetworkOverride{
			IfaceID:     fmt.Sprintf(ifaceIDFmt, i),
			HostDevName: nc.TAP,
		})
	}
	return overrides
}
