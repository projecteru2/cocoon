package cloudhypervisor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
)

// DirectClone clones from a local snapshot dir. Per-type: hardlink memory-range-*,
// reflink/copy COW, plain copy metadata; cidata is regenerated.
func (ch *CloudHypervisor) DirectClone(ctx context.Context, vmID string, vmCfg *types.VMConfig, networkConfigs []*types.NetworkConfig, snapshotConfig *types.SnapshotConfig, srcDir string) (*types.VM, error) {
	return ch.DirectCloneBase(ctx, vmID, vmCfg, networkConfigs, snapshotConfig, srcDir, cloneSnapshotFiles, ch.cloneAfterExtract)
}

func (ch *CloudHypervisor) DirectRestore(ctx context.Context, vmRef string, vmCfg *types.VMConfig, srcDir string) (*types.VM, error) {
	return ch.DirectRestoreSequence(ctx, vmRef, hypervisor.DirectRestoreSpec{
		VMCfg:     vmCfg,
		SrcDir:    srcDir,
		Preflight: ch.preflightRestore,
		Kill:      ch.killForRestore,
		Populate:  populateRunDirFromSrc,
		AfterExtract: func(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *hypervisor.VMRecord) (*types.VM, error) {
			directBoot := isDirectBoot(rec.BootConfig)
			return ch.restoreAfterExtract(ctx, vmID, vmCfg, rec, directBoot, ch.cowPath(vmID, directBoot))
		},
	})
}

func populateRunDirFromSrc(rec *hypervisor.VMRecord, srcDir string) error {
	if err := cleanSnapshotFiles(rec.RunDir); err != nil {
		return fmt.Errorf("clean old snapshot files: %w", err)
	}
	if err := cloneSnapshotFiles(rec.RunDir, srcDir); err != nil {
		return fmt.Errorf("clone snapshot files: %w", err)
	}
	return nil
}

func cloneSnapshotFiles(dstDir, srcDir string) error {
	chCfg, _, err := parseCHConfig(filepath.Join(srcDir, "config.json"))
	if err != nil {
		return fmt.Errorf("parse source config: %w", err)
	}
	cowFiles := identifyCOWFiles(chCfg)
	return hypervisor.CloneSnapshotFiles(dstDir, srcDir, func(name string) hypervisor.SnapshotFileKind {
		switch {
		case strings.HasPrefix(name, "memory-range"):
			return hypervisor.SnapshotFileMemory
		case cowFiles[name]:
			return hypervisor.SnapshotFileCOW
		default:
			return hypervisor.SnapshotFileMeta
		}
	})
}

// cleanSnapshotFiles enumerates by name so stale data-*.raw and cocoon.json
// from a previous incarnation don't linger; COW files are overwritten anyway.
func cleanSnapshotFiles(runDir string) error {
	return hypervisor.CleanSnapshotFiles(runDir, func(name string) bool {
		switch {
		case strings.HasPrefix(name, "memory-range"):
			return true
		case name == "config.json" || name == "state.json":
			return true
		case name == hypervisor.SnapshotMetaFile:
			return true
		case hypervisor.IsDataDiskFile(name):
			return true
		}
		return false
	})
}

func identifyCOWFiles(cfg *chVMConfig) map[string]bool {
	m := make(map[string]bool)
	for _, d := range cfg.Disks {
		if !d.ReadOnly {
			m[filepath.Base(d.Path)] = true
		}
	}
	return m
}
