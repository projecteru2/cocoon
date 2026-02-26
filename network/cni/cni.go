package cni

import (
	"context"
	"fmt"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/lock/flock"
	"github.com/projecteru2/cocoon/storage"
	storejson "github.com/projecteru2/cocoon/storage/json"
	"github.com/projecteru2/cocoon/types"
)

const typ = "cni"

// CNI implements network.Network using CNI plugins with per-VM netns + bridge + tap.
type CNI struct {
	conf   *config.Config
	store  storage.Store[networkIndex]
	locker lock.Locker
}

// New creates a CNI network provider.
func New(conf *config.Config) (*CNI, error) {
	if err := conf.EnsureCNIDirs(); err != nil {
		return nil, fmt.Errorf("ensure cni dirs: %w", err)
	}
	locker := flock.New(conf.CNIIndexLock())
	store := storejson.New[networkIndex](conf.CNIIndexFile(), locker)
	return &CNI{conf: conf, store: store, locker: locker}, nil
}

func (c *CNI) Type() string { return typ }

// Inspect returns the network record for a single network ID.
// Returns (nil, nil) if not found.
func (c *CNI) Inspect(ctx context.Context, id string) (*types.Network, error) {
	var result *types.Network
	return result, c.store.With(ctx, func(idx *networkIndex) error {
		rec := idx.Networks[id]
		if rec == nil {
			return nil
		}
		net := rec.Network // value copy
		result = &net
		return nil
	})
}

// List returns all known network records.
func (c *CNI) List(ctx context.Context) ([]*types.Network, error) {
	var result []*types.Network
	return result, c.store.With(ctx, func(idx *networkIndex) error {
		for _, rec := range idx.Networks {
			if rec == nil {
				continue
			}
			net := rec.Network
			result = append(result, &net)
		}
		return nil
	})
}

// Config, Delete — to be implemented.

func (c *CNI) Config(_ context.Context, _ []*types.VMConfig) ([][]*types.NetworkConfig, error) {
	panic("not implemented")
}

func (c *CNI) Delete(_ context.Context, _ []string) ([]string, error) {
	panic("not implemented")
}
