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
		if err != nil {
			logger.Warnf(ctx, "skip %s: TryLock error: %v", m.getName(), err)
			skipped = append(skipped, m.getName())
			continue
		}
		if !ok {
			logger.Warnf(ctx, "skip %s: lock held by another operation", m.getName())
			skipped = append(skipped, m.getName())
			continue
		}
		locked = append(locked, m)
	}
	defer func() {
		for _, m := range locked {
			m.getLocker().Unlock(ctx) //nolint:errcheck,gosec
		}
	}()

	// Fail-closed: if any module was skipped, abort the entire cycle.
	// Collecting without a complete cross-module snapshot risks deleting data
	// still protected by the missing module (e.g. blobs pinned by VMs).
	if len(skipped) > 0 {
		return fmt.Errorf("gc aborted: modules skipped (lock busy): %s", strings.Join(skipped, ", "))
	}

	// Phase 1: snapshot all locked modules.
	snapshots := make(map[string]any, len(locked))
	for _, m := range locked {
		snap, err := m.readSnapshot(ctx)
		if err != nil {
			return fmt.Errorf("gc aborted: snapshot %s: %w", m.getName(), err)
		}
		snapshots[m.getName()] = snap
	}

	// Phase 2: resolve deletion targets (cross-module via snapshots).
	targets := make(map[string][]string)
	for _, m := range locked {
		if ids := m.resolveTargets(snapshots[m.getName()], snapshots); len(ids) > 0 {
			targets[m.getName()] = ids
		}
	}

	// Phase 3: collect (skip modules with no targets).
	var errs []string
	for _, m := range locked {
		ids := targets[m.getName()]
		if len(ids) == 0 {
			continue
		}
		if err := m.collect(ctx, ids); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", m.getName(), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("gc errors: %s", strings.Join(errs, "; "))
	}
	return nil
}
