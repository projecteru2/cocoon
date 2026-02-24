package cloudhypervisor

import (
	"context"
	"fmt"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/lock/flock"
	"github.com/projecteru2/cocoon/storage"
	storejson "github.com/projecteru2/cocoon/storage/json"
	"github.com/projecteru2/cocoon/types"
)

const typ = "cloud-hypervisor"

// CloudHypervisor implements hypervisor.Hypervisor using the Cloud Hypervisor VMM.
type CloudHypervisor struct {
	conf   *config.Config
	store  storage.Store[hypervisor.VMIndex]
	locker lock.Locker
}

// New creates a CloudHypervisor backend.
func New(conf *config.Config) (*CloudHypervisor, error) {
	if err := conf.EnsureCHDirs(); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}
	locker := flock.New(conf.CHIndexLock())
	store := storejson.New[hypervisor.VMIndex](conf.CHIndexFile(), locker)
	return &CloudHypervisor{conf: conf, store: store, locker: locker}, nil
}

func (ch *CloudHypervisor) Type() string { return typ }

// Inspect returns the VMInfo for a single VM by ID.
// Returns (nil, nil) if the VM is not found.
// Runtime fields (PID, SocketPath) are populated from the PID file and config.
func (ch *CloudHypervisor) Inspect(ctx context.Context, id string) (*types.VMInfo, error) {
	var result *types.VMInfo
	return result, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		rec := idx.VMs[id]
		if rec == nil {
			return nil
		}
		info := rec.VMInfo // value copy — detached from the DB record
		ch.enrichRuntime(&info)
		result = &info
		return nil
	})
}

// List returns VMInfo for all known VMs.
// Runtime fields are populated for each entry.
func (ch *CloudHypervisor) List(ctx context.Context) ([]*types.VMInfo, error) {
	var result []*types.VMInfo
	return result, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		for _, rec := range idx.VMs {
			if rec == nil {
				continue
			}
			info := rec.VMInfo
			ch.enrichRuntime(&info)
			result = append(result, &info)
		}
		return nil
	})
}

// Delete removes VM records from the index and returns the IDs that were deleted.
// Unknown IDs are silently skipped. Runtime and log directories are left for GC.
func (ch *CloudHypervisor) Delete(ctx context.Context, ids []string) ([]string, error) {
	var deleted []string
	return deleted, ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		for _, id := range ids {
			if _, ok := idx.VMs[id]; ok {
				delete(idx.VMs, id)
				deleted = append(deleted, id)
			}
		}
		return nil
	})
}

func (ch *CloudHypervisor) Create(_ context.Context, _ *types.VMConfig, _ []*types.StorageConfig, _ *types.BootConfig) (*types.VMInfo, error) {
	panic("not implemented")
}
