package cloudhypervisor

import (
	"context"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/hypervisor"
)

// compile-time interface check.
var _ hypervisor.Hypervisor = (*CloudHypervisor)(nil)

// chSnapshot is the typed GC snapshot for the Cloud Hypervisor backend.
// Its sole purpose is to expose the union of all VMs' ImageBlobIDs so that
// image GC modules can skip blobs still needed by active VMs.
type chSnapshot struct {
	blobIDs map[string]struct{} // union of all VMs' ImageBlobIDs
}

// UsedBlobIDs implements gc.UsedBlobIDs.
func (s chSnapshot) UsedBlobIDs() map[string]struct{} { return s.blobIDs }

// GCModule returns a typed gc.Module[chSnapshot] for the Cloud Hypervisor backend.
//
// ReadDB (under lock): collects all image blob IDs referenced by VMs.
// Resolve/Collect are no-ops — VM residual cleanup is done by Delete directly.
// The module exists purely as a data provider for cross-module GC.
func (ch *CloudHypervisor) GCModule() gc.Module[chSnapshot] {
	return gc.Module[chSnapshot]{
		Name:   typ,
		Locker: ch.locker,
		ReadDB: func(ctx context.Context) (chSnapshot, error) {
			var snap chSnapshot
			if err := ch.store.Read(func(idx *hypervisor.VMIndex) error {
				snap.blobIDs = make(map[string]struct{})
				for _, rec := range idx.VMs {
					if rec == nil {
						continue
					}
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
		Resolve: func(_ chSnapshot, _ map[string]any) []string { return nil },
		Collect: func(_ context.Context, _ []string) error { return nil },
	}
}

// RegisterGC registers the Cloud Hypervisor GC module with the given Orchestrator.
func (ch *CloudHypervisor) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, ch.GCModule())
}
