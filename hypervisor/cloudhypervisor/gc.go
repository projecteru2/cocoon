package cloudhypervisor

import (
	"context"
	"errors"
	"time"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

const creatingStateGCGrace = 24 * time.Hour

type chSnapshot struct {
	blobIDs     map[string]struct{} // union of all VMs' ImageBlobIDs
	vmIDs       map[string]struct{} // all VM IDs in the DB
	staleCreate []string            // IDs in stale "creating" state (crash remnants)
}

func (s chSnapshot) UsedBlobIDs() map[string]struct{} { return s.blobIDs }

// GCModule returns the GC module for cross-module blob pinning and orphan cleanup.
func (ch *CloudHypervisor) GCModule() gc.Module[chSnapshot] {
	return gc.Module[chSnapshot]{
		Name:   typ,
		Locker: ch.locker,
		ReadDB: func(_ context.Context) (chSnapshot, error) {
			var snap chSnapshot
			cutoff := time.Now().Add(-creatingStateGCGrace)
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
					if rec.State == types.VMStateCreating && rec.UpdatedAt.Before(cutoff) {
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
			// "db" is a reserved system subdirectory (stores vms.json/vms.lock).
			// When RootDir == RunDir, it lives alongside per-VM dirs and must be
			// excluded from orphan detection.
			reserved := map[string]struct{}{"db": {}}
			runOrphans := utils.FilterUnreferenced(utils.ScanSubdirs(ch.conf.CHRunDir()), snap.vmIDs, reserved)
			logOrphans := utils.FilterUnreferenced(utils.ScanSubdirs(ch.conf.CHLogDir()), snap.vmIDs, reserved)
			candidates := append(append(runOrphans, logOrphans...), snap.staleCreate...)
			seen := make(map[string]struct{}, len(candidates))
			var result []string
			for _, id := range candidates {
				if _, ok := seen[id]; ok {
					continue
				}
				seen[id] = struct{}{}
				result = append(result, id)
			}
			return result
		},
		Collect: func(ctx context.Context, ids []string) error {
			var errs []error
			// Remove orphan directories (best-effort for dirs that may not exist).
			for _, id := range ids {
				if err := ch.removeVMDirs(ctx, id); err != nil {
					errs = append(errs, err)
				}
			}
			// Clean up stale "creating" DB records from this GC snapshot.
			if err := ch.cleanStalePlaceholders(ctx, ids); err != nil {
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	}
}

// cleanStalePlaceholders removes selected DB records stuck in stale "creating"
// state. IDs not found (or no longer stale) are skipped.
func (ch *CloudHypervisor) cleanStalePlaceholders(_ context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	targets := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		targets[id] = struct{}{}
	}
	cutoff := time.Now().Add(-creatingStateGCGrace)
	return ch.store.Write(func(idx *hypervisor.VMIndex) error {
		for id := range targets {
			rec := idx.VMs[id]
			if rec == nil {
				continue
			}
			if rec.State != types.VMStateCreating || rec.UpdatedAt.After(cutoff) {
				continue
			}
			delete(idx.Names, rec.Config.Name)
			delete(idx.VMs, id)
		}
		return nil
	})
}

// RegisterGC registers the Cloud Hypervisor GC module with the given Orchestrator.
func (ch *CloudHypervisor) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, ch.GCModule())
}
