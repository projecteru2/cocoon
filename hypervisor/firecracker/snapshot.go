package firecracker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

const (
	snapshotVMStateFile = "vmstate"
	snapshotMemFile     = "mem"
)

// Snapshot pauses the VM, captures its full state (CPU/device state via FC
// snapshot API + memory file + COW disk via reflink copy), resumes the VM,
// and returns a streaming tar.gz reader of the snapshot directory.
func (fc *Firecracker) Snapshot(ctx context.Context, ref string) (*types.SnapshotConfig, io.ReadCloser, error) {
	logger := log.WithFunc("firecracker.Snapshot")

	vmID, err := fc.ResolveRef(ctx, ref)
	if err != nil {
		return nil, nil, err
	}

	rec, err := fc.LoadRecord(ctx, vmID)
	if err != nil {
		return nil, nil, err
	}
	if vErr := types.ValidateStorageConfigs(rec.StorageConfigs); vErr != nil {
		return nil, nil, fmt.Errorf("storage invariants violated: %w", vErr)
	}

	sockPath := hypervisor.SocketPath(rec.RunDir)
	hc := utils.NewSocketHTTPClient(sockPath)
	cowPath := fc.conf.COWRawPath(vmID)

	tmpDir, err := os.MkdirTemp(fc.conf.VMRunDir(vmID), "snapshot-")
	if err != nil {
		return nil, nil, fmt.Errorf("create temp dir: %w", err)
	}

	// Lock every writable disk so a concurrent clone's rename+symlink
	// redirect can't race this snapshot's reflink. Dictionary order keeps
	// concurrent callers deadlock-free.
	if err := withSourceWritableDisksLocked(rec.StorageConfigs, func() error {
		return fc.WithRunningVM(ctx, &rec, func(_ int) error {
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

			if err := createSnapshotFC(ctx, sockPath, tmpDir); err != nil {
				return fmt.Errorf("snapshot: %w", err)
			}
			if err := utils.ReflinkCopy(filepath.Join(tmpDir, cowFileName), cowPath); err != nil {
				return fmt.Errorf("copy COW: %w", err)
			}

			if err := hypervisor.ReflinkDataDisks(tmpDir, rec.StorageConfigs); err != nil {
				return err
			}

			doResume()
			if resumeErr != nil {
				return fmt.Errorf("snapshot data captured but resume failed: %w", resumeErr)
			}
			return nil
		})
	}); err != nil {
		os.RemoveAll(tmpDir) //nolint:errcheck,gosec
		return nil, nil, fmt.Errorf("snapshot VM %s: %w", vmID, err)
	}

	// Save snapshot metadata (absolute paths) so clones can reconstruct
	// storage/boot config without depending on live VM records.
	// FC snapshots require the same directory layout — paths are stored as-is.
	if metaErr := hypervisor.SaveSnapshotMeta(tmpDir, buildSnapshotMeta(&rec)); metaErr != nil {
		os.RemoveAll(tmpDir) //nolint:errcheck,gosec
		return nil, nil, fmt.Errorf("save snapshot metadata: %w", metaErr)
	}

	snapID, recErr := fc.RecordSnapshot(ctx, vmID, tmpDir)
	if recErr != nil {
		return nil, nil, recErr
	}

	return fc.BuildSnapshotConfig(snapID, &rec), utils.TarDirStreamWithRemove(tmpDir), nil
}

// buildSnapshotMeta builds the FC sidecar from a live VM record. Storage
// configs are deep-copied (subsequent VM-record mutations would otherwise
// taint the on-disk JSON) and the kernel path is rewritten to vmlinuz so
// clones receive the portable artifact, not the FC-specific vmlinux cache.
func buildSnapshotMeta(rec *hypervisor.VMRecord) *hypervisor.SnapshotMeta {
	meta := &hypervisor.SnapshotMeta{CPU: rec.Config.CPU, Memory: rec.Config.Memory}
	for _, sc := range rec.StorageConfigs {
		cp := *sc
		meta.StorageConfigs = append(meta.StorageConfigs, &cp)
	}
	if rec.BootConfig != nil {
		b := *rec.BootConfig
		if filepath.Base(b.KernelPath) == "vmlinux" {
			b.KernelPath = filepath.Join(filepath.Dir(b.KernelPath), "vmlinuz")
		}
		b.Cmdline = "" // cmdline is rebuilt on clone with new VM name/IP
		meta.BootConfig = &b
	}
	return meta
}

// loadSnapshotMeta reads metadata and validates paths are in cocoon-managed
// dirs — FC accepts paths from snapshot files imported from other hosts, so
// the validator rejects symlink/escape attempts before opening the files.
func loadSnapshotMeta(dir, rootDir, runDir string) (*hypervisor.SnapshotMeta, error) {
	meta, err := hypervisor.LoadSnapshotMeta(dir)
	if err != nil {
		return nil, err
	}
	for _, sc := range meta.StorageConfigs {
		if !isUnderDir(sc.Path, rootDir) && !isUnderDir(sc.Path, runDir) {
			return nil, fmt.Errorf("untrusted storage path in snapshot metadata: %s", sc.Path)
		}
	}
	if b := meta.BootConfig; b != nil {
		if b.KernelPath != "" && !isUnderDir(b.KernelPath, rootDir) {
			return nil, fmt.Errorf("untrusted kernel path in snapshot metadata: %s", b.KernelPath)
		}
		if b.InitrdPath != "" && !isUnderDir(b.InitrdPath, rootDir) {
			return nil, fmt.Errorf("untrusted initrd path in snapshot metadata: %s", b.InitrdPath)
		}
	}
	return meta, nil
}

func isUnderDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	cleaned := filepath.Clean(path)
	root := filepath.Clean(dir)
	return strings.HasPrefix(cleaned, root+string(filepath.Separator))
}
