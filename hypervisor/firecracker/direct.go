package firecracker

import (
	"context"
	"strings"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
)

// DirectClone clones from a local snapshot dir. Per-type: hardlink mem,
// reflink/copy COW, plain copy metadata.
func (fc *Firecracker) DirectClone(ctx context.Context, vmID string, vmCfg *types.VMConfig, networkConfigs []*types.NetworkConfig, snapshotConfig *types.SnapshotConfig, srcDir string) (*types.VM, error) {
	return fc.DirectCloneBase(ctx, vmID, vmCfg, networkConfigs, snapshotConfig, srcDir, cloneSnapshotFiles, fc.cloneAfterExtract)
}

func (fc *Firecracker) DirectRestore(ctx context.Context, vmRef string, vmCfg *types.VMConfig, srcDir string) (*types.VM, error) {
	return fc.DirectRestoreSequence(ctx, vmRef, hypervisor.DirectRestoreSpec{
		VMCfg:         vmCfg,
		SrcDir:        srcDir,
		OverrideCheck: validateRestoreOverrides,
		Preflight:     fc.preflightRestore,
		Kill:          fc.killForRestore,
		// Lock writable disks so recoverStaleBackup heals stale data-*.raw.cocoon-clone-backup
		// before restore overwrites them; otherwise a future clone renames backup over restored data.
		Wrap: func(rec *hypervisor.VMRecord, inner func() error) error {
			return withSourceWritableDisksLocked(rec.StorageConfigs, inner)
		},
		Populate: func(rec *hypervisor.VMRecord, srcDir string) error {
			return hypervisor.PopulateFromSrc(rec.RunDir, srcDir, cleanSnapshotFiles, cloneSnapshotFiles)
		},
		AfterExtract: func(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *hypervisor.VMRecord) (*types.VM, error) {
			return fc.restoreAfterExtract(ctx, vmID, vmCfg, rec, fc.conf.COWRawPath(vmID))
		},
	})
}

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

// cleanSnapshotFiles enumerates by name so stale data-*.raw don't linger across restore.
func cleanSnapshotFiles(runDir string) error {
	return hypervisor.CleanSnapshotFiles(runDir, func(name string) bool {
		switch name {
		case snapshotVMStateFile, snapshotMemFile, cowFileName, hypervisor.SnapshotMetaFile:
			return true
		}
		return hypervisor.IsDataDiskFile(name)
	})
}
