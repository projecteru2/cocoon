package gc

import (
	"context"
	"fmt"
	"strings"

	"github.com/projecteru2/core/log"
)

// Orchestrator runs GC across all registered modules.
type Orchestrator struct {
	modules []runner
}

// New creates an empty Orchestrator.
func New() *Orchestrator { return &Orchestrator{} }

// Register adds a typed Module to the Orchestrator.
// This is a package-level function (not a method) because Go methods cannot
// have type parameters.
func Register[S any](o *Orchestrator, m Module[S]) {
	o.modules = append(o.modules, m)
}

// Run executes one GC cycle:
//
//  1. TryLock all modules; skip those whose lock is busy.
//  2. ReadDB each locked module to build a snapshot.
//  3. Resolve deletion targets per module. Each module's Resolve receives
//     all other snapshots (typed as any) for cross-module analysis — e.g.,
//     image GC checks UsedBlobIDs from the VM snapshot to protect active blobs.
//  4. Collect targets for each snapshotted module.
//  5. Unlock all (deferred).
//
// All locks are held for the entire cycle so that the snapshot, resolve, and
// collect phases see a consistent view. GC runs infrequently and executes
// fast, so the extended lock hold is acceptable.
func (o *Orchestrator) Run(ctx context.Context) error {
	logger := log.WithFunc("gc.Run")

	// Acquire all locks up front; hold until GC finishes.
	var locked []runner
	var skipped []string
	for _, m := range o.modules {
		ok, err := m.getLocker().TryLock(ctx)
		if err != nil || !ok {
			logger.Warnf(ctx, "skip %s: lock busy", m.getName())
			skipped = append(skipped, m.getName())
			continue
		}
		locked = append(locked, m)
	}
	defer func() {
		for _, m := range locked {
			m.getLocker().Unlock(ctx) //nolint:errcheck
		}
	}()

	// Phase 1: snapshot all locked modules.
	snapshots := make(map[string]any, len(locked))
	for _, m := range locked {
		snap, err := m.readSnapshot(ctx)
		if err != nil {
			logger.Warnf(ctx, "snapshot %s: %v", m.getName(), err)
			continue
		}
		snapshots[m.getName()] = snap
	}

	// Phase 2: resolve deletion targets (cross-module via snapshots).
	targets := make(map[string][]string)
	for _, m := range locked {
		snap, ok := snapshots[m.getName()]
		if !ok {
			continue
		}
		if ids := m.resolveTargets(snap, snapshots); len(ids) > 0 {
			targets[m.getName()] = ids
		}
	}

	// Phase 3: collect.
	var errs []string
	for _, m := range locked {
		if _, snapshotted := snapshots[m.getName()]; !snapshotted {
			continue
		}
		ids := targets[m.getName()]
		if err := m.collect(ctx, ids); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", m.getName(), err))
		}
	}
	if len(skipped) > 0 {
		errs = append(errs, fmt.Sprintf("skipped (lock busy): %s", strings.Join(skipped, ", ")))
	}
	if len(errs) > 0 {
		return fmt.Errorf("gc errors: %s", strings.Join(errs, "; "))
	}
	return nil
}
