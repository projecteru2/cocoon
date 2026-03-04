package network

import (
	"context"
	"errors"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/types"
)

var (
	ErrNotFound      = errors.New("network not found")
	ErrNotConfigured = errors.New("network provider not configured")
)

type Network interface {
	Type() string

	// Verify checks whether the network namespace for a VM exists.
	// Returns nil if the netns is present, an error otherwise.
	Verify(ctx context.Context, vmID string) error
	// Config creates network namespace, bridge, and tap for a VM.
	// When existing configs are provided (recovery after host reboot),
	// the netns and tap devices are recreated using the persisted MAC addresses.
	Config(ctx context.Context, vmID string, numNICs int, vmCfg *types.VMConfig, existing ...*types.NetworkConfig) ([]*types.NetworkConfig, error)
	Delete(context.Context, []string) ([]string, error)
	Inspect(context.Context, string) (*types.Network, error)
	List(context.Context) ([]*types.Network, error)

	RegisterGC(*gc.Orchestrator)
}
