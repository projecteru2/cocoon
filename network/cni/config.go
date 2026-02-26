package cni

import (
	"context"
	"fmt"

	"github.com/projecteru2/cocoon/network"
	"github.com/projecteru2/cocoon/types"
)

func (c *CNI) Config(_ context.Context, _ []*types.VMConfig) ([][]*types.NetworkConfig, error) {
	if c.networkConfList == nil || c.cniConf == nil {
		return nil, fmt.Errorf("%w: no conflist found in %s", network.ErrNotConfigured, c.conf.CNIConfDir)
	}
	panic("not implemented")
}
