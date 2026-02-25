package network

import (
	"context"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/types"
)

type Network interface {
	Type() string

	Config(context.Context, []*types.VMConfig) ([][]*types.NetworkConfig, error)
	Delete(context.Context, []string) ([]string, error)
	Inspect(context.Context, string) (*types.Network, error)
	List() ([]*types.Network, error)

	RegisterGC(*gc.Orchestrator)
}
