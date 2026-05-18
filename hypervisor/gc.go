package hypervisor

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/cocoonstack/cocoon/gc"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// VMGCSnapshot is the ReadDB-phase data for any hypervisor GC module (CH + FC share the shape).
type VMGCSnapshot struct {
	blobIDs     map[string]struct{}
	vmIDs       map[string]struct{}
	staleCreate []string
	runDirs     []string
	logDirs     []string
}

func (s VMGCSnapshot) UsedBlobIDs() map[string]struct{} { return s.blobIDs }

func (s VMGCSnapshot) ActiveVMIDs() map[string]struct{} { return s.vmIDs }

// BuildGCModule builds GC module that scans DB and dirs for orphan VMs.
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
					maps.Copy(snap.blobIDs, rec.ImageBlobIDs)
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
		Resolve: func(_ context.Context, snap VMGCSnapshot, _ map[string]any) []string {
			// "db" holds vms.json/vms.lock — exclude from orphan scan when RootDir == RunDir.
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

func (b *Backend) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, b.BuildGCModule())
}

// WatchPath returns VM index file path for filesystem-based watching.
func (b *Backend) WatchPath() string {
	return b.Conf.IndexFile()
}

// GCCollect kills leftover hypervisor processes and removes orphan dirs/records.
// Runs under the GC orchestrator's flock — uses lock-free DB access.
func (b *Backend) GCCollect(ctx context.Context, ids []string) error {
	var errs []error
	for _, id := range ids {
		runDir, logDir := b.Conf.VMRunDir(id), b.Conf.VMLogDir(id)
		_ = b.DB.ReadRaw(func(idx *VMIndex) error {
			if rec := idx.VMs[id]; rec != nil {
				runDir, logDir = rec.RunDir, rec.LogDir
			}
			return nil
		})
		b.killOrphanProcess(ctx, runDir)
		if err := RemoveVMDirs(runDir, logDir); err != nil {
			errs = append(errs, err)
		}
	}
	if err := b.CleanStalePlaceholders(ctx, ids); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// killOrphanProcess terminates a leftover hypervisor process if PID matches the binary.
func (b *Backend) killOrphanProcess(ctx context.Context, runDir string) {
	pid, err := utils.ReadPIDFile(b.PIDFilePath(runDir))
	if err != nil {
		return
	}
	sockPath := SocketPath(runDir)
	if !utils.VerifyProcessCmdline(pid, b.Conf.BinaryName(), sockPath) {
		return
	}
	_ = utils.TerminateProcess(ctx, pid, b.Conf.BinaryName(), sockPath, b.Conf.TerminateGracePeriod())
}
