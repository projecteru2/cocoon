package firecracker

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
	return fc.SnapshotSequence(ctx, ref, hypervisor.SnapshotSpec{
		Pause:  func(_ *hypervisor.VMRecord, hc *http.Client) error { return pauseVM(ctx, hc) },
		Resume: func(_ *hypervisor.VMRecord, hc *http.Client) error { return resumeVM(context.WithoutCancel(ctx), hc) },
		// createSnapshotFC builds its own client with VMMemTransferTimeout
		// (multi-GiB memory transfer); it cannot share hc.
		Capture: func(rec *hypervisor.VMRecord, _ *http.Client, tmpDir string) error {
			sockPath := hypervisor.SocketPath(rec.RunDir)
			if err := createSnapshotFC(ctx, sockPath, tmpDir); err != nil {
				return fmt.Errorf("snapshot: %w", err)
			}
			if err := utils.ReflinkCopy(filepath.Join(tmpDir, cowFileName), fc.conf.COWRawPath(rec.ID)); err != nil {
				return fmt.Errorf("copy COW: %w", err)
			}
			return hypervisor.ReflinkDataDisks(tmpDir, rec.StorageConfigs)
		},
		// Lock writable disks: a concurrent clone's rename+symlink redirect
		// would otherwise race this snapshot's reflink. Dictionary order
		// avoids deadlock.
		Wrap: func(rec *hypervisor.VMRecord, fn func() error) error {
			return withSourceWritableDisksLocked(rec.StorageConfigs, fn)
		},
		BuildMeta: buildSnapshotMeta,
	})
}

// buildSnapshotMeta rewrites kernel path to vmlinuz so clones get the portable
// artifact instead of the FC-specific vmlinux cache.
func buildSnapshotMeta(rec *hypervisor.VMRecord, _ string) (*hypervisor.SnapshotMeta, error) {
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
	return meta, nil
}
