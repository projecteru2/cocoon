package cni

import (
	"context"

	"github.com/projecteru2/cocoon/types"
)

func (c *CNI) Config(_ context.Context, _ []*types.VMConfig) ([][]*types.NetworkConfig, error) {
	panic("not implemented")
}
