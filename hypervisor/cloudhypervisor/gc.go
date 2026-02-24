package cloudhypervisor

import (
	"context"
	"errors"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// compile-time interface check.
var _ hypervisor.Hypervisor = (*CloudHypervisor)(nil)

type chSnapshot struct {
	blobIDs     map[string]struct{} // union of all VMs' ImageBlobIDs
	vmIDs       map[string]struct{} // all VM IDs in the DB
	staleCreate []string            // IDs in "creating" state (crash remnants)
}

func (s chSnapshot) UsedBlobIDs() map[string]struct{} { return s.blobIDs }

// GCModule returns the GC module for cross-module blob pinning and orphan cleanup.
func (ch *CloudHypervisor) GCModule() gc.Module[chSnapshot] {
	return gc.Module[chSnapshot]{
		Name:   typ,
		Locker: ch.locker,
		ReadDB: func(_ context.Context) (chSnapshot, error) {
			var snap chSnapshot
			if err := ch.store.Read(func(idx *hypervisor.VMIndex) error {
				snap.blobIDs = make(map[string]struct{})
				snap.vmIDs = make(map[string]struct{})
				for id, rec := range idx.VMs {
					if rec == nil {
						continue
					}
					snap.vmIDs[id] = struct{}{}
					for hex := range rec.ImageBlobIDs {
						snap.blobIDs[hex] = struct{}{}
					}
					if rec.State == types.VMStateCreating {
						snap.staleCreate = append(snap.staleCreate, id)
					}
				}
				return nil
			}); err != nil {
				return snap, err
			}
			return snap, nil
		},
		Resolve: func(snap chSnapshot, _ map[string]any) []string {
			// Orphan directories not in the DB.
			orphans := utils.FilterUnreferenced(utils.ScanSubdirs(ch.conf.CHRunDir()), snap.vmIDs)
			// Stale "creating" records from interrupted Create calls.
			return append(orphans, snap.staleCreate...)
		},
		Collect: func(ctx context.Context, ids []string) error {
			var errs []error
			// Remove orphan directories (best-effort for dirs that may not exist).
			for _, id := range ids {
				if err := ch.removeVMDirs(ctx, id); err != nil {
					errs = append(errs, err)
				}
			}
			// Clean up stale "creating" DB records.
			if err := ch.cleanStalePlaceholders(ctx); err != nil {
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	}
}

// cleanStalePlaceholders removes DB records stuck in "creating" state.
func (ch *CloudHypervisor) cleanStalePlaceholders(_ context.Context) error {
	return ch.store.Write(func(idx *hypervisor.VMIndex) error {
		for id, rec := range idx.VMs {
			if rec != nil && rec.State == types.VMStateCreating {
				delete(idx.Names, rec.Config.Name)
				delete(idx.VMs, id)
			}
		}
		return nil
	})
}

// RegisterGC registers the Cloud Hypervisor GC module with the given Orchestrator.
func (ch *CloudHypervisor) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, ch.GCModule())
}
