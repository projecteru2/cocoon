package cloudhypervisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// snapshotMetaFile re-exports the shared cocoon sidecar filename so the
// CH-internal call sites stay unchanged after the consolidation.
// CH's sidecar mirrors chCfg.Disks order and length so each entry pairs
// with the correct disk slot during patchCHConfig.
const snapshotMetaFile = hypervisor.SnapshotMetaFile

// Snapshot pauses the VM, captures its full state (CPU, memory, devices via CH
// snapshot API, plus the COW disk via sparse copy), resumes the VM, and returns
// a streaming tar.gz reader of the snapshot directory.
func (ch *CloudHypervisor) Snapshot(ctx context.Context, ref string) (*types.SnapshotConfig, io.ReadCloser, error) {
	logger := log.WithFunc("cloudhypervisor.Snapshot")

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

	// Determine COW file path and name inside the tar archive.
	directBoot := isDirectBoot(rec.BootConfig)
	cowPath := ch.cowPath(vmID, directBoot)
	cowName := "overlay.qcow2"
	if directBoot {
		cowName = "cow.raw"
	}

	// Create a temporary directory for the snapshot data.
	tmpDir, err := os.MkdirTemp(ch.conf.VMRunDir(vmID), "snapshot-")
	if err != nil {
		return nil, nil, fmt.Errorf("create temp dir: %w", err)
	}

	// WithRunningVM verifies the process is alive, then runs the callback.
	// Inside the callback: pause → CH snapshot → SparseCopy COW → resume.
	if err := ch.WithRunningVM(ctx, &rec, func(_ int) error {
		if err := pauseVM(ctx, hc); err != nil {
			return fmt.Errorf("pause: %w", err)
		}

		resumed := false
		var resumeErr error
		doResume := func() {
			if resumed {
				return
			}
			resumed = true
			resumeErr = resumeVM(context.WithoutCancel(ctx), hc)
			if resumeErr != nil {
				logger.Warnf(ctx, "resume VM %s: %v", vmID, resumeErr)
			}
		}
		defer doResume()

		if err := snapshotVM(ctx, hc, tmpDir); err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}

		if err := utils.ReflinkCopy(filepath.Join(tmpDir, cowName), cowPath); err != nil {
			return fmt.Errorf("copy COW: %w", err)
		}

		if err := hypervisor.ReflinkDataDisks(tmpDir, rec.StorageConfigs); err != nil {
			return err
		}

		// Resume eagerly so we can propagate the error.
		// The deferred doResume is a no-op when resumed=true.
		doResume()
		if resumeErr != nil {
			return fmt.Errorf("snapshot data captured but resume failed: %w", resumeErr)
		}
		return nil
	}); err != nil {
		os.RemoveAll(tmpDir) //nolint:errcheck,gosec
		return nil, nil, fmt.Errorf("snapshot VM %s: %w", vmID, err)
	}

	// For cloudimg VMs, include cidata.img (per-VM cloud-init disk).
	// cidata is read-only and static, so it can be copied outside the pause window.
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

	// Generate snapshot ID and record it on the VM atomically.
	snapID, recErr := ch.RecordSnapshot(ctx, vmID, tmpDir)
	if recErr != nil {
		return nil, nil, recErr
	}

	return ch.BuildSnapshotConfig(snapID, &rec), utils.TarDirStreamWithRemove(tmpDir), nil
}

// writeSnapshotMeta builds the cocoon.json sidecar by mirroring the
// snapshot's config.json shape. Using activeDisks(rec) would diverge for a
// cloudimg VM snapshotted post-FirstBooted but pre-restart: CH still holds
// cidata, but activeDisks would skip it.
func writeSnapshotMeta(tmpDir string, rec *hypervisor.VMRecord) error {
	chCfg, _, err := parseCHConfig(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		return fmt.Errorf("parse snapshot config: %w", err)
	}
	byPath := make(map[string]*types.StorageConfig, len(rec.StorageConfigs))
	for _, sc := range rec.StorageConfigs {
		byPath[sc.Path] = sc
	}
	storage := make([]*types.StorageConfig, 0, len(chCfg.Disks))
	for _, d := range chCfg.Disks {
		sc, ok := byPath[d.Path]
		if !ok {
			return fmt.Errorf("snapshot config has disk %q not present in VM record", d.Path)
		}
		cp := *sc
		storage = append(storage, &cp)
	}
	return hypervisor.SaveSnapshotMeta(tmpDir, &hypervisor.SnapshotMeta{
		StorageConfigs: storage,
		BootConfig:     rec.BootConfig,
	})
}
