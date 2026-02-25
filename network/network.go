package network

import (
	"context"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/types"
)

type Network interface {
	Type() string

	Config(context.Context, []*types.VM) ([][]*types.NetworkConfig, error)
	Inspect(context.Context, string) (*types.Network, error)
	List() ([]Network, error)

	RegisterGC(*gc.Orchestrator)
}
