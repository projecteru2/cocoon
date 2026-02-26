package network

import (
	"context"
	"errors"

	"github.com/projecteru2/cocoon/types"
)

var (
	ErrNotFound      = errors.New("network not found")
	ErrNotConfigured = errors.New("network provider not configured")
)

type Network interface {
	Type() string

	Config(context.Context, []*types.VMConfig) ([][]*types.NetworkConfig, error)
	Delete(context.Context, []string) ([]string, error)
	Inspect(context.Context, string) (*types.Network, error)
	List(context.Context) ([]*types.Network, error)
}
