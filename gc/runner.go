package gc

import (
	"context"

	"github.com/projecteru2/cocoon/lock"
)

// runner is the internal interface Orchestrator uses to hold heterogeneous
// Module[S] values. Unexported — callers work with Module[S] and Register.
type runner interface {
	getName() string
	getLocker() lock.Locker
	readSnapshot(ctx context.Context) (any, error)
	resolveTargets(snap any, others map[string]any) []string
	collect(ctx context.Context, ids []string) error
}
