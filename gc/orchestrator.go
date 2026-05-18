package gc

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/projecteru2/core/log"
)

// Orchestrator runs GC across all registered modules.
type Orchestrator struct {
	modules []runner
}

// New creates an empty Orchestrator.
func New() *Orchestrator { return &Orchestrator{} }

// Register adds a typed Module; package-level (not a method) because Go methods can't have type params.
func Register[S any](o *Orchestrator, m Module[S]) {
	o.modules = append(o.modules, m)
}

// Run executes one GC cycle: lock all modules, snapshot, resolve, collect.
// Fail-closed: any busy lock aborts the cycle so cross-module decisions stay consistent.
func (o *Orchestrator) Run(ctx context.Context) error {
	start := time.Now()
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

	// Fail-closed: skip aborts the cycle so cross-module references (e.g. VMs pinning blobs) aren't violated.
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
		if ids := m.resolveTargets(ctx, snapshots[m.getName()], snapshots); len(ids) > 0 {
			targets[m.getName()] = ids
		}
	}

	// Phase 3: collect (skip modules with no targets).
	var errs []error
	summary := make(map[string]int, len(locked))
	failures := 0
	for _, m := range locked {
		ids := targets[m.getName()]
		if len(ids) == 0 {
			continue
		}
		if err := m.collect(ctx, ids, snapshots[m.getName()]); err != nil {
			failures++
			errs = append(errs, fmt.Errorf("gc %s: %w", m.getName(), err))
		}
		summary[m.getName()] = len(ids)
	}
	logger.Infof(ctx, "completed: %s (failures: %d, duration: %s)",
		formatSummary(summary), failures, time.Since(start).Truncate(time.Millisecond))
	return errors.Join(errs...)
}

// formatSummary renders the per-module collection counts as `m1=N m2=M`, sorted by module name.
func formatSummary(s map[string]int) string {
	if len(s) == 0 {
		return "nothing to collect"
	}
	keys := slices.Sorted(maps.Keys(s))
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%s=%d", k, s[k])
	}
	return sb.String()
}
