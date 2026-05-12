package network

import (
	"context"
	"errors"

	"github.com/cocoonstack/cocoon/gc"
	"github.com/cocoonstack/cocoon/types"
)

var (
	ErrNotFound      = errors.New("network not found")
	ErrNotConfigured = errors.New("network provider not configured")
)

// AddSpec is one NIC's add request; Existing != nil reuses MAC/IP for recovery.
type AddSpec struct {
	Index    int
	Existing *types.NetworkConfig
}

type Network interface {
	Type() string
	Verify(ctx context.Context, vmID string) error
	Add(ctx context.Context, vmID string, vmCfg *types.VMConfig, specs ...AddSpec) ([]*types.NetworkConfig, error)
	Remove(ctx context.Context, vmID string, indices ...int) error
	Delete(context.Context, []string) ([]string, error)
	Inspect(context.Context, string) (*types.Network, error)
	List(context.Context) ([]*types.Network, error)
	RegisterGC(*gc.Orchestrator)
}

func AddRange(from, count int) []AddSpec {
	out := make([]AddSpec, count)
	for i := range out {
		out[i] = AddSpec{Index: from + i}
	}
	return out
}

func AddRecover(existing []*types.NetworkConfig) []AddSpec {
	out := make([]AddSpec, len(existing))
	for i, e := range existing {
		out[i] = AddSpec{Index: i, Existing: e}
	}
	return out
}
