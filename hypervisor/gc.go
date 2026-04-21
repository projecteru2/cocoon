package hypervisor

import (
	"context"
	"slices"
	"time"

	"github.com/cocoonstack/cocoon/gc"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// VMGCSnapshot holds the data collected during the ReadDB phase of a
// hypervisor GC module. Both Cloud Hypervisor and Firecracker produce
// identical snapshots; the type lives here to avoid duplication.
type VMGCSnapshot struct {
	blobIDs     map[string]struct{}
	vmIDs       map[string]struct{}
	staleCreate []string
	runDirs     []string
	logDirs     []string
}

// UsedBlobIDs returns blob IDs referenced by all VMs in this backend.
func (s VMGCSnapshot) UsedBlobIDs() map[string]struct{} { return s.blobIDs }

// ActiveVMIDs returns IDs of all VMs in this backend's database.
func (s VMGCSnapshot) ActiveVMIDs() map[string]struct{} { return s.vmIDs }

// BuildGCModule returns a gc.Module that scans this backend's DB and
// directories for orphan VMs and stale placeholders.
func (b *Backend) BuildGCModule() gc.Module[VMGCSnapshot] {
	return gc.Module[VMGCSnapshot]{
		Name:   b.Typ,
		Locker: b.Locker,
		ReadDB: func(_ context.Context) (VMGCSnapshot, error) {
			var snap VMGCSnapshot
			cutoff := time.Now().Add(-CreatingStateGCGrace)
			if err := b.DB.ReadRaw(func(idx *VMIndex) error {
				snap.blobIDs = make(map[string]struct{}, len(idx.VMs))
				snap.vmIDs = make(map[string]struct{}, len(idx.VMs))
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
			var err error
			if snap.runDirs, err = utils.ScanSubdirs(b.Conf.RunDir()); err != nil {
				return snap, err
			}
			if snap.logDirs, err = utils.ScanSubdirs(b.Conf.LogDir()); err != nil {
				return snap, err
			}
			return snap, nil
		},
		Resolve: func(snap VMGCSnapshot, _ map[string]any) []string {
			// "db" is a reserved system subdirectory (stores vms.json/vms.lock).
			// When RootDir == RunDir, it lives alongside per-VM dirs and must be
			// excluded from orphan detection.
			reserved := map[string]struct{}{"db": {}}
			runOrphans := utils.FilterUnreferenced(snap.runDirs, snap.vmIDs, reserved)
			logOrphans := utils.FilterUnreferenced(snap.logDirs, snap.vmIDs, reserved)
			candidates := slices.Concat(runOrphans, logOrphans, snap.staleCreate)
			slices.Sort(candidates)
			return slices.Compact(candidates)
		},
		Collect: b.GCCollect,
	}
}
