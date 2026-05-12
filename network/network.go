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

// Network defines the interface for a network provider (CNI, bridge, ...).
type Network interface {
	Type() string

	// Verify checks whether the network namespace / TAPs for a VM exist.
	Verify(ctx context.Context, vmID string) error

	// Add allocates NIC host plumbing for the given specs; idempotent re: netns.
	Add(ctx context.Context, vmID string, vmCfg *types.VMConfig, specs ...AddSpec) ([]*types.NetworkConfig, error)

	// Remove tears down NIC host plumbing for the given indices; preserves netns.
	Remove(ctx context.Context, vmID string, indices ...int) error

	Delete(context.Context, []string) ([]string, error)
	Inspect(context.Context, string) (*types.Network, error)
	List(context.Context) ([]*types.Network, error)

	RegisterGC(*gc.Orchestrator)
}

// AddRange returns specs for a contiguous range of fresh NICs.
func AddRange(from, count int) []AddSpec {
	out := make([]AddSpec, count)
	for i := range out {
		out[i] = AddSpec{Index: from + i}
	}
	return out
}

// AddRecover returns specs for re-creating existing NICs at slots 0..len-1.
func AddRecover(existing []*types.NetworkConfig) []AddSpec {
	out := make([]AddSpec, len(existing))
	for i, e := range existing {
		out[i] = AddSpec{Index: i, Existing: e}
	}
	return out
}
