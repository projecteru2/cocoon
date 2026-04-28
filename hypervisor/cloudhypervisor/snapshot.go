package cloudhypervisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// Snapshot pauses, captures CH state+COW, resumes, and streams the result.
func (ch *CloudHypervisor) Snapshot(ctx context.Context, ref string) (*types.SnapshotConfig, io.ReadCloser, error) {
	vmID, err := ch.ResolveRef(ctx, ref)
	if err != nil {
		return nil, nil, err
	}

	rec, err := ch.LoadRecord(ctx, vmID)
	if err != nil {
		return nil, nil, err
	}
	if vErr := types.ValidateStorageConfigs(rec.StorageConfigs); vErr != nil {
		return nil, nil, fmt.Errorf("storage invariants violated: %w", vErr)
	}

	sockPath := hypervisor.SocketPath(rec.RunDir)
	hc := utils.NewSocketHTTPClient(sockPath)

	directBoot := isDirectBoot(rec.BootConfig)
	cowPath := ch.cowPath(vmID, directBoot)
	cowName := filepath.Base(cowPath)

	tmpDir, err := os.MkdirTemp(ch.conf.VMRunDir(vmID), "snapshot-")
	if err != nil {
		return nil, nil, fmt.Errorf("create temp dir: %w", err)
	}

	pause := func() error { return pauseVM(ctx, hc) }
	resume := func() error { return resumeVM(context.WithoutCancel(ctx), hc) }
	if err := ch.WithPausedVM(ctx, &rec, pause, resume, func() error {
		if err := snapshotVM(ctx, hc, tmpDir); err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}
		if err := utils.ReflinkCopy(filepath.Join(tmpDir, cowName), cowPath); err != nil {
			return fmt.Errorf("copy COW: %w", err)
		}
		return hypervisor.ReflinkDataDisks(tmpDir, rec.StorageConfigs)
	}); err != nil {
		os.RemoveAll(tmpDir) //nolint:errcheck,gosec
		return nil, nil, fmt.Errorf("snapshot VM %s: %w", vmID, err)
	}

	// cidata is RO + static — safe to copy outside the pause window.
	if !directBoot && !rec.Config.Windows {
		cidataSrc := ch.conf.CidataPath(vmID)
		if _, statErr := os.Stat(cidataSrc); statErr == nil {
			if cpErr := utils.SparseCopy(filepath.Join(tmpDir, cidataFile), cidataSrc); cpErr != nil {
				os.RemoveAll(tmpDir) //nolint:errcheck,gosec
				return nil, nil, fmt.Errorf("copy cidata: %w", cpErr)
			}
		}
	}

	if metaErr := writeSnapshotMeta(tmpDir, &rec); metaErr != nil {
		os.RemoveAll(tmpDir) //nolint:errcheck,gosec
		return nil, nil, fmt.Errorf("write snapshot metadata: %w", metaErr)
	}

	snapID, recErr := ch.RecordSnapshot(ctx, vmID, tmpDir)
	if recErr != nil {
		return nil, nil, recErr
	}

	return ch.BuildSnapshotConfig(snapID, &rec), utils.TarDirStreamWithRemove(tmpDir), nil
}

// writeSnapshotMeta mirrors config.json's disk shape. activeDisks(rec) would
// diverge for cloudimg post-FirstBooted but pre-restart: CH still holds cidata,
// activeDisks would skip it.
func writeSnapshotMeta(tmpDir string, rec *hypervisor.VMRecord) error {
	chCfg, _, err := parseCHConfig(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		return fmt.Errorf("parse snapshot config: %w", err)
	}
	byPath := make(map[string]*types.StorageConfig, len(rec.StorageConfigs))
	for _, sc := range rec.StorageConfigs {
		byPath[sc.Path] = sc
	}
	ordered := make([]*types.StorageConfig, 0, len(chCfg.Disks))
	for _, d := range chCfg.Disks {
		sc, ok := byPath[d.Path]
		if !ok {
			return fmt.Errorf("snapshot config has disk %q not present in VM record", d.Path)
		}
		ordered = append(ordered, sc)
	}
	return hypervisor.SaveSnapshotMeta(tmpDir, &hypervisor.SnapshotMeta{
		StorageConfigs: hypervisor.CloneStorageConfigs(ordered),
		BootConfig:     rec.BootConfig,
	})
}
