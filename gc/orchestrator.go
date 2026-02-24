package gc

import (
	"context"
	"fmt"
	"strings"
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
//  1. For each module: TryLock → ReadDB → Unlock (skip if busy).
//  2. Each module's Resolve analyses its typed snapshot, with other modules'
//     snapshots available as map[string]any for cross-module analysis.
//  3. For each module with targets: TryLock → Collect → Unlock (skip if busy).
//
// Step 3 re-acquires the lock rather than holding it from step 1 to keep
// contention minimal. commitAndRecord validates blob existence under lock
// before writing the index, so a deletion racing with a commit is caught
// there and the pull retries.
func (o *Orchestrator) Run(ctx context.Context) error {
	// Phase 1: collect each module's snapshot under lock.
	snapshots := make(map[string]any, len(o.modules))
	for _, m := range o.modules {
		ok, err := m.getLocker().TryLock(ctx)
		if err != nil || !ok {
			continue
		}
		snap, readErr := m.readSnapshot(ctx)
		m.getLocker().Unlock(ctx) //nolint:errcheck
		if readErr != nil {
			continue
		}
		snapshots[m.getName()] = snap
	}

	// Phase 2: resolve deletion targets per module (no locks held).
	// Each module sees its own snapshot typed, others as any.
	targets := make(map[string][]string)
	for _, m := range o.modules {
		snap, ok := snapshots[m.getName()]
		if !ok {
			continue
		}
		if ids := m.resolveTargets(snap, snapshots); len(ids) > 0 {
			targets[m.getName()] = ids
		}
	}
	if len(targets) == 0 {
		return nil
	}

	// Phase 3: collect under lock, skipping busy modules.
	var errs []string
	for _, m := range o.modules {
		ids := targets[m.getName()]
		if len(ids) == 0 {
			continue
		}
		ok, err := m.getLocker().TryLock(ctx)
		if err != nil || !ok {
			continue
		}
		collectErr := m.collect(ctx, ids)
		m.getLocker().Unlock(ctx) //nolint:errcheck
		if collectErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", m.getName(), collectErr))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("gc errors: %s", strings.Join(errs, "; "))
	}
	return nil
}
