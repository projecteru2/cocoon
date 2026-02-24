package gc

import (
	"context"

	"github.com/projecteru2/cocoon/lock"
)

// Module[S] describes a typed storage module that participates in GC.
// S is the snapshot type returned by ReadDB and consumed by Resolve,
// giving the Resolve implementation full type safety on its own data.
type Module[S any] struct {
	Name   string
	Locker lock.Locker

	// ReadDB reads the module's current state (called while the lock is held).
	ReadDB func(ctx context.Context) (S, error)

	// Resolve analyses this module's typed snapshot and returns IDs to delete.
	// others contains snapshots from all other modules (typed as any).
	// Use type assertions on others for cross-module analysis (e.g. vm pinning images).
	Resolve func(snap S, others map[string]any) []string

	// Collect removes the given IDs (called while the lock is held).
	Collect func(ctx context.Context, ids []string) error
}

// Module[S] implements runner — internal to the gc package.
func (m Module[S]) getName() string        { return m.Name }
func (m Module[S]) getLocker() lock.Locker { return m.Locker }

func (m Module[S]) readSnapshot(ctx context.Context) (any, error) {
	return m.ReadDB(ctx)
}

func (m Module[S]) resolveTargets(snap any, others map[string]any) []string {
	return m.Resolve(snap.(S), others)
}

func (m Module[S]) collect(ctx context.Context, ids []string) error {
	return m.Collect(ctx, ids)
}
