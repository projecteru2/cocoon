package hypervisor

import (
	"context"
	"fmt"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// ReserveVM inserts a "creating" placeholder under id, failing on id/name collision.
func (b *Backend) ReserveVM(ctx context.Context, id string, vmCfg *types.VMConfig, blobIDs map[string]struct{}, runDir, logDir string) error {
	now := time.Now()
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		if idx.VMs[id] != nil {
			return fmt.Errorf("id collision %q (retry)", id)
		}
		if dup, ok := idx.Names[vmCfg.Name]; ok {
			return fmt.Errorf("vm name %q already exists (id: %s)", vmCfg.Name, dup)
		}
		idx.VMs[id] = &VMRecord{
			VM: types.VM{
				ID: id, Hypervisor: b.Typ, State: types.VMStateCreating,
				Config: *vmCfg, CreatedAt: now, UpdatedAt: now,
			},
			ImageBlobIDs: blobIDs,
			RunDir:       runDir,
			LogDir:       logDir,
		}
		idx.Names[vmCfg.Name] = id
		return nil
	})
}

// RollbackCreate removes a placeholder VM record from the DB.
func (b *Backend) RollbackCreate(ctx context.Context, id, name string) {
	if err := b.DB.Update(ctx, func(idx *VMIndex) error {
		delete(idx.VMs, id)
		if name != "" && idx.Names[name] == id {
			delete(idx.Names, name)
		}
		return nil
	}); err != nil {
		log.WithFunc(b.Typ+".RollbackCreate").Errorf(ctx, err, "rollback VM %s (name=%s)", id, name)
	}
}

// FinalizeCreate writes a populated VM record to DB, replacing the placeholder.
func (b *Backend) FinalizeCreate(ctx context.Context, id string, info *types.VM, bootCfg *types.BootConfig, blobIDs map[string]struct{}) error {
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		existing, err := idx.GetRecord(id)
		if err != nil {
			return err
		}
		idx.VMs[id] = &VMRecord{
			VM:           *info,
			BootConfig:   bootCfg,
			ImageBlobIDs: blobIDs,
			RunDir:       existing.RunDir,
			LogDir:       existing.LogDir,
		}
		return nil
	})
}

// CreateSequence is the shared create skeleton. The placeholder-then-finalize
// shape lets a crash mid-create leave a rolled-back DB and rundir, so GC
// has nothing stale to reconcile.
func (b *Backend) CreateSequence(ctx context.Context, id string, spec CreateSpec) (_ *types.VM, err error) {
	if err = ValidateHostCPU(spec.VMCfg.CPU); err != nil {
		return nil, err
	}

	now := time.Now()
	runDir := b.Conf.VMRunDir(id)
	logDir := b.Conf.VMLogDir(id)
	blobIDs := ExtractBlobIDs(spec.StorageConfigs, spec.BootConfig)

	defer func() {
		if err != nil {
			_ = RemoveVMDirs(runDir, logDir)
			b.RollbackCreate(ctx, id, spec.VMCfg.Name)
		}
	}()

	if err = b.ReserveVM(ctx, id, spec.VMCfg, blobIDs, runDir, logDir); err != nil {
		return nil, fmt.Errorf("reserve VM record: %w", err)
	}
	if err = utils.EnsureDirs(runDir, logDir); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}

	var bootCopy *types.BootConfig
	if spec.BootConfig != nil {
		bc := *spec.BootConfig
		bootCopy = &bc
	}

	preparedStorage, err := spec.Prepare(ctx, id, spec.VMCfg, spec.StorageConfigs, spec.Net, bootCopy)
	if err != nil {
		return nil, err
	}
	if err = types.ValidateStorageConfigs(preparedStorage); err != nil {
		return nil, fmt.Errorf("storage invariants violated: %w", err)
	}

	info := &types.VM{
		ID: id, Hypervisor: b.Typ, State: types.VMStateCreated,
		Config: *spec.VMCfg, StorageConfigs: preparedStorage,
		NetSetup:  spec.Net,
		CreatedAt: now, UpdatedAt: now,
	}
	if err = b.FinalizeCreate(ctx, id, info, bootCopy, blobIDs); err != nil {
		return nil, fmt.Errorf("finalize VM record: %w", err)
	}
	return info, nil
}
