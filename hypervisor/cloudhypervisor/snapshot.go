package cloudhypervisor

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"

	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// snapshotReader wraps io.PipeReader so that Close waits for the background
// goroutine to finish streaming, then synchronously removes tmpDir.
// streamErr captures the goroutine's error so Close can surface it even
// after all data has been read (PipeReader.Close always returns nil).
type snapshotReader struct {
	*io.PipeReader
	done   <-chan error
	tmpDir string // cleaned up synchronously in Close, not by the goroutine
}

func (r *snapshotReader) Close() error {
	err := r.PipeReader.Close()
	if streamErr := <-r.done; streamErr != nil {
		err = streamErr
	}
	// Synchronous cleanup: goroutine has finished all I/O at this point,
	// so RemoveAll runs in the caller's stack — no race with process exit.
	if r.tmpDir != "" {
		os.RemoveAll(r.tmpDir) //nolint:errcheck,gosec
		r.tmpDir = ""          // prevent double-remove
	}
	return err
}

// Snapshot pauses the VM, captures its full state (CPU, memory, devices via CH
// snapshot API, plus the COW disk via sparse copy), resumes the VM, and returns
// a streaming tar.gz reader of the snapshot directory.
func (ch *CloudHypervisor) Snapshot(ctx context.Context, ref string) (*types.SnapshotConfig, io.ReadCloser, error) {
	logger := log.WithFunc("cloudhypervisor.Snapshot")

	vmID, err := ch.resolveRef(ctx, ref)
	if err != nil {
		return nil, nil, err
	}

	rec, err := ch.loadRecord(ctx, vmID)
	if err != nil {
		return nil, nil, err
	}

	sockPath := socketPath(rec.RunDir)
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

	// withRunningVM verifies the process is alive, then runs the callback.
	// Inside the callback: pause → CH snapshot → SparseCopy COW → resume.
	if err := ch.withRunningVM(ctx, &rec, func(_ int) error {
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

		if err := utils.SparseCopy(filepath.Join(tmpDir, cowName), cowPath); err != nil {
			return fmt.Errorf("sparse copy COW: %w", err)
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

	// Generate snapshot ID and record it on the VM atomically.
	snapID, genErr := utils.GenerateID()
	if genErr != nil {
		os.RemoveAll(tmpDir) //nolint:errcheck,gosec
		return nil, nil, fmt.Errorf("generate snapshot ID: %w", genErr)
	}
	if updateErr := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		r := idx.VMs[vmID]
		if r == nil {
			return fmt.Errorf("VM %s disappeared from index", vmID)
		}
		if r.SnapshotIDs == nil {
			r.SnapshotIDs = make(map[string]struct{})
		}
		r.SnapshotIDs[snapID] = struct{}{}
		return nil
	}); updateErr != nil {
		os.RemoveAll(tmpDir) //nolint:errcheck,gosec
		return nil, nil, fmt.Errorf("record snapshot on VM: %w", updateErr)
	}

	// Build SnapshotConfig from the VM record.
	cfg := &types.SnapshotConfig{
		ID:      snapID,
		Image:   rec.Config.Image,
		CPU:     rec.Config.CPU,
		Memory:  rec.Config.Memory,
		Storage: rec.Config.Storage,
		NICs:    len(rec.NetworkConfigs),
		Windows: rec.Config.Windows,
	}
	if rec.ImageBlobIDs != nil {
		cfg.ImageBlobIDs = make(map[string]struct{}, len(rec.ImageBlobIDs))
		maps.Copy(cfg.ImageBlobIDs, rec.ImageBlobIDs)
	}

	// Stream tmpDir as tar via io.Pipe. The goroutine handles streaming only;
	// tmpDir cleanup is done synchronously in snapshotReader.Close() after the
	// goroutine signals completion — no race with process exit.
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var streamErr error
		defer func() {
			if streamErr != nil {
				pw.CloseWithError(streamErr) //nolint:errcheck,gosec
			} else {
				pw.Close() //nolint:errcheck,gosec
			}
			done <- streamErr
		}()

		tw := tar.NewWriter(pw)

		streamErr = utils.TarDir(tw, tmpDir)
		if closeErr := tw.Close(); streamErr == nil {
			streamErr = closeErr
		}
	}()

	return cfg, &snapshotReader{PipeReader: pr, done: done, tmpDir: tmpDir}, nil
}
