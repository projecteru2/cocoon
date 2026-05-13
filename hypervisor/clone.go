package hypervisor

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// CloneSetup is the shared pre-clone sequence: validate CPU, reserve a
// placeholder, ensure dirs, return a cleanup that rolls back both.
func (b *Backend) CloneSetup(ctx context.Context, vmID string, vmCfg *types.VMConfig, snapshotConfig *types.SnapshotConfig) (runDir, logDir string, now time.Time, cleanup func(), err error) {
	if err = ValidateHostCPU(vmCfg.CPU); err != nil {
		return "", "", time.Time{}, nil, err
	}
	now = time.Now()
	runDir = b.Conf.VMRunDir(vmID)
	logDir = b.Conf.VMLogDir(vmID)

	cleanup = func() {
		_ = RemoveVMDirs(runDir, logDir)
		b.RollbackCreate(ctx, vmID, vmCfg.Name)
	}

	if err = b.ReserveVM(ctx, vmID, vmCfg, snapshotConfig.ImageBlobIDs, runDir, logDir); err != nil {
		return "", "", time.Time{}, nil, fmt.Errorf("reserve VM record: %w", err)
	}
	if err = utils.EnsureDirs(runDir, logDir); err != nil {
		cleanup()
		return "", "", time.Time{}, nil, fmt.Errorf("ensure dirs: %w", err)
	}
	return runDir, logDir, now, cleanup, nil
}

// DirectCloneBase clones from a local snapshot directory. Used when the
// snapshot lives on the same host (no tar streaming needed).
func (b *Backend) DirectCloneBase(
	ctx context.Context, vmID string, vmCfg *types.VMConfig,
	net types.NetSetup, snapshotConfig *types.SnapshotConfig, srcDir string,
	cloneFiles func(dstDir, srcDir string) error,
	afterExtract func(ctx context.Context, vmID string, vmCfg *types.VMConfig, net types.NetSetup, runDir, logDir string, now time.Time) (*types.VM, error),
) (_ *types.VM, err error) {
	runDir, logDir, now, cleanup, err := b.CloneSetup(ctx, vmID, vmCfg, snapshotConfig)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	if err = cloneFiles(runDir, srcDir); err != nil {
		return nil, fmt.Errorf("clone snapshot files: %w", err)
	}

	return afterExtract(ctx, vmID, vmCfg, net, runDir, logDir, now)
}

// CloneFromStream clones from a tar stream into a fresh runDir. Used when
// the snapshot arrives over the network (cross-node clone).
func (b *Backend) CloneFromStream(
	ctx context.Context, vmID string, vmCfg *types.VMConfig,
	net types.NetSetup, snapshotConfig *types.SnapshotConfig, snapshot io.Reader,
	afterExtract func(ctx context.Context, vmID string, vmCfg *types.VMConfig, net types.NetSetup, runDir, logDir string, now time.Time) (*types.VM, error),
) (_ *types.VM, err error) {
	runDir, logDir, now, cleanup, err := b.CloneSetup(ctx, vmID, vmCfg, snapshotConfig)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	if err = utils.ExtractTar(runDir, snapshot); err != nil {
		return nil, fmt.Errorf("extract snapshot: %w", err)
	}

	return afterExtract(ctx, vmID, vmCfg, net, runDir, logDir, now)
}

// FinalizeClone updates the cloned VM's record in place after restore-and-resume.
func (b *Backend) FinalizeClone(ctx context.Context, vmID string, info *types.VM, bootCfg *types.BootConfig, blobIDs map[string]struct{}) error {
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		r, err := idx.GetRecord(vmID)
		if err != nil {
			return err
		}
		r.VM = *info
		r.BootConfig = bootCfg
		r.FirstBooted = true
		if blobIDs != nil {
			r.ImageBlobIDs = blobIDs
		}
		return nil
	})
}
