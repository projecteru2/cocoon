package cloudhypervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/utils"
)

// compile-time interface check.
var _ hypervisor.Hypervisor = (*CloudHypervisor)(nil)

// chSnapshot is the typed GC snapshot for the Cloud Hypervisor backend.
type chSnapshot struct {
	vmIDs   map[string]struct{} // VM IDs present in the DB index
	runDirs []string            // VM IDs that have a runtime dir on disk
	logDirs []string            // VM IDs that have a log dir on disk
}

// GCModule returns a typed gc.Module[chSnapshot] for the Cloud Hypervisor backend.
//
// ReadDB (under lock): reads the VM index and scans the runtime/log base dirs
// on disk, returning a snapshot of what exists vs what is recorded.
//
// Resolve: returns VM IDs whose runtime or log dirs are orphaned (present on
// disk but no longer in the DB).
//
// Collect (under lock): removes the orphaned dirs for each VM ID.
func (ch *CloudHypervisor) GCModule() gc.Module[chSnapshot] {
	return gc.Module[chSnapshot]{
		Name:   typ,
		Locker: ch.locker,
		ReadDB: func(ctx context.Context) (chSnapshot, error) {
			var snap chSnapshot
			if err := ch.store.Read(func(idx *hypervisor.VMIndex) error {
				snap.vmIDs = make(map[string]struct{}, len(idx.VMs))
				for id := range idx.VMs {
					snap.vmIDs[id] = struct{}{}
				}
				return nil
			}); err != nil {
				return snap, err
			}
			snap.runDirs = utils.ScanSubdirs(filepath.Join(ch.conf.RunDir, "cloudhypervisor"))
			snap.logDirs = utils.ScanSubdirs(filepath.Join(ch.conf.LogDir, "cloudhypervisor"))
			return snap, nil
		},
		Resolve: func(snap chSnapshot, _ map[string]any) []string {
			unreferenced := utils.FilterUnreferenced(snap.runDirs, snap.vmIDs)
			seen := make(map[string]struct{}, len(unreferenced))
			for _, id := range unreferenced {
				seen[id] = struct{}{}
			}
			for _, id := range utils.FilterUnreferenced(snap.logDirs, snap.vmIDs) {
				if _, already := seen[id]; !already {
					unreferenced = append(unreferenced, id)
				}
			}
			return unreferenced
		},
		Collect: func(ctx context.Context, ids []string) error {
			var errs []error
			for _, vmID := range ids {
				if err := os.RemoveAll(ch.conf.CHVMRunDir(vmID)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
				if err := os.RemoveAll(ch.conf.CHVMLogDir(vmID)); err != nil && !os.IsNotExist(err) {
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
