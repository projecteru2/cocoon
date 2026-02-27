package cloudhypervisor

import (
	"context"
	"errors"
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

// CloudHypervisor implements hypervisor.Hypervisor.
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

// Inspect returns VM for a single VM by ref (ID, name, or prefix).
func (ch *CloudHypervisor) Inspect(ctx context.Context, ref string) (*types.VM, error) {
	var result *types.VM
	return result, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		id, err := hypervisor.ResolveVMRef(idx, ref)
		if err != nil {
			return err
		}
		result = ch.toVM(idx.VMs[id])
		return nil
	})
}

// List returns VM for all known VMs.
func (ch *CloudHypervisor) List(ctx context.Context) ([]*types.VM, error) {
	var result []*types.VM
	return result, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		for _, rec := range idx.VMs {
			if rec == nil {
				continue
			}
			result = append(result, ch.toVM(rec))
		}
		return nil
	})
}

// Delete removes VMs. Running VMs require force=true (stops them first).
func (ch *CloudHypervisor) Delete(ctx context.Context, refs []string, force bool) ([]string, error) {
	ids, err := ch.resolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	return forEachVM(ctx, ids, "Delete", true, func(ctx context.Context, id string) error {
		if err := ch.withRunningVM(id, func(_ int) error {
			if !force {
				return fmt.Errorf("running (force required)")
			}
			return ch.stopOne(ctx, id)
		}); err != nil && !errors.Is(err, hypervisor.ErrNotRunning) {
			return fmt.Errorf("stop before delete: %w", err)
		}
		if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
			rec := idx.VMs[id]
			if rec == nil {
				return hypervisor.ErrNotFound
			}
			delete(idx.Names, rec.Config.Name)
			delete(idx.VMs, id)
			return nil
		}); err != nil {
			return err
		}
		if err := ch.removeVMDirs(ctx, id); err != nil {
			return fmt.Errorf("cleanup VM dirs: %w", err)
		}
		return nil
	})
}

// resolveRefs batch-resolves refs to exact VM IDs under a single lock.
// Duplicate refs that resolve to the same ID are silently deduplicated.
func (ch *CloudHypervisor) resolveRefs(ctx context.Context, refs []string) ([]string, error) {
	var ids []string
	return ids, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		seen := make(map[string]struct{}, len(refs))
		for _, ref := range refs {
			id, err := hypervisor.ResolveVMRef(idx, ref)
			if err != nil {
				return fmt.Errorf("resolve %q: %w", ref, err)
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		return nil
	})
}
