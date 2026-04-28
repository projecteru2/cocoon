package firecracker

import (
	"context"
	"fmt"
	"strings"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
)

// DirectClone creates a new VM from a local snapshot directory.
// Files are handled per-type: hardlink for mem, reflink/copy for
// the COW disk, plain copy for small metadata (vmstate).
func (fc *Firecracker) DirectClone(ctx context.Context, vmID string, vmCfg *types.VMConfig, networkConfigs []*types.NetworkConfig, snapshotConfig *types.SnapshotConfig, srcDir string) (*types.VM, error) {
	return fc.DirectCloneBase(ctx, vmID, vmCfg, networkConfigs, snapshotConfig, srcDir, cloneSnapshotFiles, fc.cloneAfterExtract)
}

// DirectRestore reverts a running VM using a local snapshot directory.
// Files are handled per-type: hardlink for mem, reflink/copy for
// the COW disk, plain copy for small metadata (vmstate).
func (fc *Firecracker) DirectRestore(ctx context.Context, vmRef string, vmCfg *types.VMConfig, srcDir string) (*types.VM, error) {
	return fc.DirectRestoreSequence(ctx, vmRef, hypervisor.DirectRestoreSpec{
		VMCfg:         vmCfg,
		SrcDir:        srcDir,
		OverrideCheck: validateRestoreOverrides,
		Preflight:     fc.preflightRestore,
		Kill:          fc.killForRestore,
		// Lock every writable disk so recoverStaleBackup heals stale
		// data-*.raw.cocoon-clone-backup before restore overwrites them;
		// otherwise a future clone would rename the backup over restored data.
		Wrap: func(rec *hypervisor.VMRecord, inner func() error) error {
			return withSourceWritableDisksLocked(rec.StorageConfigs, inner)
		},
		Populate: populateRunDirFromSrc,
		AfterExtract: func(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *hypervisor.VMRecord) (*types.VM, error) {
			return fc.restoreAfterExtract(ctx, vmID, vmCfg, rec, fc.conf.COWRawPath(vmID))
		},
	})
}

// populateRunDirFromSrc cleans rec.RunDir of old snapshot files then copies
// new ones from srcDir using the per-backend cloneSnapshotFiles strategy.
func populateRunDirFromSrc(rec *hypervisor.VMRecord, srcDir string) error {
	if err := cleanSnapshotFiles(rec.RunDir); err != nil {
		return fmt.Errorf("clean old snapshot files: %w", err)
	}
	if err := cloneSnapshotFiles(rec.RunDir, srcDir); err != nil {
		return fmt.Errorf("clone snapshot files: %w", err)
	}
	return nil
}

// cloneSnapshotFiles copies snapshot files from srcDir to dstDir using
// per-file strategies: hardlink/symlink for mem, reflink/sparse for COW,
// plain copy for metadata (vmstate).
func cloneSnapshotFiles(dstDir, srcDir string) error {
	return hypervisor.CloneSnapshotFiles(dstDir, srcDir, func(name string) hypervisor.SnapshotFileKind {
		switch {
		case name == snapshotMemFile:
			return hypervisor.SnapshotFileMemory
		case strings.HasSuffix(name, ".raw"):
			return hypervisor.SnapshotFileCOW
		default:
			return hypervisor.SnapshotFileMeta
		}
	})
}

// cleanSnapshotFiles removes snapshot-specific files from runDir.
// Data disks (data-<name>.raw) are listed explicitly so stale survivors
// don't linger across restore.
func cleanSnapshotFiles(runDir string) error {
	return hypervisor.CleanSnapshotFiles(runDir, func(name string) bool {
		switch name {
		case snapshotVMStateFile, snapshotMemFile, cowFileName, hypervisor.SnapshotMetaFile:
			return true
		}
		return hypervisor.IsDataDiskFile(name)
	})
}
