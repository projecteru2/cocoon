package gc

import (
	"context"
	"fmt"
	"strings"

	"github.com/projecteru2/cocoon/lock"
)

// Snapshot is the opaque DB state read from a module while the lock is held.
// Each module's ReadDB returns its own concrete type; Resolver sees them as any.
type Snapshot = any

// Module describes a storage module that participates in garbage collection.
type Module struct {
	Name string

	// Locker is used by GC to coordinate with active operations (e.g. pull).
	// TryLock returns false when another operation is in progress; GC skips the
	// module and retries on the next run.
	Locker lock.Locker

	// ReadDB reads the module's current index state.
	// Called while the lock is held — must not re-acquire it.
	ReadDB func(ctx context.Context) (Snapshot, error)

	// Collect removes the given resource IDs.
	// Called while the lock is held — must not re-acquire it.
	Collect func(ctx context.Context, ids []string) error
}

// Resolver analyses snapshots from all successfully-read modules and returns
// the resource IDs to delete per module.
// key = Module.Name, value = IDs to pass to that module's Collect.
type Resolver func(snapshots map[string]Snapshot) map[string][]string

// Orchestrator runs GC across all registered modules.
type Orchestrator struct {
	modules  []Module
	resolver Resolver
}

// New creates an Orchestrator with the given cross-module Resolver.
func New(resolver Resolver) *Orchestrator {
	return &Orchestrator{resolver: resolver}
}

// Register adds a module to the GC cycle.
func (o *Orchestrator) Register(m Module) {
	o.modules = append(o.modules, m)
}

// Run executes one GC cycle:
//
//  1. For each module: TryLock → ReadDB → Unlock (skip if busy).
//  2. Resolver analyses all collected snapshots and returns deletion targets.
//  3. For each module with targets: TryLock → Collect → Unlock (skip if busy).
//
// Step 3 re-acquires the lock rather than holding it from step 1 to keep
// lock contention minimal. The window is safe: GC is conservative (only deletes
// unreferenced items), and commitAndRecord validates file existence under lock
// before writing the index, so a deletion that races with a commit is caught
// there and the pull retries.
func (o *Orchestrator) Run(ctx context.Context) error {
	snapshots := make(map[string]Snapshot, len(o.modules))

	// Phase 1: read each module's DB state under lock.
	for _, m := range o.modules {
		ok, err := m.Locker.TryLock(ctx)
		if err != nil {
			// log
			continue
		}
		if !ok {
			// log: module busy, will retry next run
			continue
		}
		snap, readErr := m.ReadDB(ctx)
		m.Locker.Unlock(ctx) //nolint:errcheck
		if readErr != nil {
			// log
			continue
		}
		snapshots[m.Name] = snap
	}

	// Phase 2: cross-module analysis — no locks held.
	targets := o.resolver(snapshots)
	if len(targets) == 0 {
		return nil
	}

	// Phase 3: collect under lock, skipping busy modules.
	var errs []string
	for _, m := range o.modules {
		ids := targets[m.Name]
		if len(ids) == 0 {
			continue
		}
		ok, err := m.Locker.TryLock(ctx)
		if err != nil || !ok {
			// log: will retry next run
			continue
		}
		collectErr := m.Collect(ctx, ids)
		m.Locker.Unlock(ctx) //nolint:errcheck
		if collectErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", m.Name, collectErr))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("gc errors: %s", strings.Join(errs, "; "))
	}
	return nil
}
