package firecracker

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

const (
	snapshotVMStateFile = "vmstate"
	snapshotMemFile     = "mem"
)

// Snapshot pauses, captures vmstate+mem+COW, resumes, and streams the result.
func (fc *Firecracker) Snapshot(ctx context.Context, ref string) (*types.SnapshotConfig, io.ReadCloser, error) {
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

	pause := func() error { return pauseVM(ctx, hc) }
	resume := func() error { return resumeVM(context.WithoutCancel(ctx), hc) }
	// Lock writable disks: a concurrent clone's rename+symlink redirect would
	// otherwise race this snapshot's reflink. Dictionary order avoids deadlock.
	if err := withSourceWritableDisksLocked(rec.StorageConfigs, func() error {
		return fc.WithPausedVM(ctx, &rec, pause, resume, func() error {
			if err := createSnapshotFC(ctx, sockPath, tmpDir); err != nil {
				return fmt.Errorf("snapshot: %w", err)
			}
			if err := utils.ReflinkCopy(filepath.Join(tmpDir, cowFileName), cowPath); err != nil {
				return fmt.Errorf("copy COW: %w", err)
			}
			return hypervisor.ReflinkDataDisks(tmpDir, rec.StorageConfigs)
		})
	}); err != nil {
		os.RemoveAll(tmpDir) //nolint:errcheck,gosec
		return nil, nil, fmt.Errorf("snapshot VM %s: %w", vmID, err)
	}

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

// buildSnapshotMeta rewrites kernel path to vmlinuz so clones get the portable
// artifact instead of the FC-specific vmlinux cache.
func buildSnapshotMeta(rec *hypervisor.VMRecord) *hypervisor.SnapshotMeta {
	meta := &hypervisor.SnapshotMeta{
		CPU:            rec.Config.CPU,
		Memory:         rec.Config.Memory,
		StorageConfigs: hypervisor.CloneStorageConfigs(rec.StorageConfigs),
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
