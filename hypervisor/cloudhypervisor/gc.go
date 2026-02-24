package cloudhypervisor

import (
	"context"
	"errors"
	"os"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/hypervisor"
)

// compile-time interface check.
var _ hypervisor.Hypervisor = (*CloudHypervisor)(nil)

type chSnapshot struct {
	blobIDs map[string]struct{} // union of all VMs' ImageBlobIDs
	vmIDs   map[string]struct{} // all VM IDs in the DB
}

func (s chSnapshot) UsedBlobIDs() map[string]struct{} { return s.blobIDs }

// GCModule returns the GC module for cross-module blob pinning and orphan cleanup.
func (ch *CloudHypervisor) GCModule() gc.Module[chSnapshot] {
	return gc.Module[chSnapshot]{
		Name:   typ,
		Locker: ch.locker,
		ReadDB: func(ctx context.Context) (chSnapshot, error) {
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
				}
				return nil
			}); err != nil {
				return snap, err
			}
			return snap, nil
		},
		Resolve: func(snap chSnapshot, _ map[string]any) []string {
			// Scan run dir for VM directories not in the DB (orphans).
			entries, err := os.ReadDir(ch.conf.CHRunDir())
			if err != nil {
				return nil
			}
			var orphans []string
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				if _, inDB := snap.vmIDs[e.Name()]; !inDB {
					orphans = append(orphans, e.Name())
				}
			}
			return orphans
		},
		Collect: func(ctx context.Context, ids []string) error {
			var errs []error
			for _, id := range ids {
				if err := os.RemoveAll(ch.conf.CHVMRunDir(id)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
				if err := os.RemoveAll(ch.conf.CHVMLogDir(id)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		},
	}
}

// RegisterGC registers the Cloud Hypervisor GC module with the given Orchestrator.
func (ch *CloudHypervisor) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, ch.GCModule())
}
