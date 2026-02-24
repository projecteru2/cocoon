package cloudhypervisor

import (
	"context"
	"fmt"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/lock/flock"
	storejson "github.com/projecteru2/cocoon/storage/json"
	"github.com/projecteru2/cocoon/storage"
	"github.com/projecteru2/cocoon/types"
)

const typ = "cloud-hypervisor"

// CloudHypervisor implements hypervisor.Hypervisor using the Cloud Hypervisor VMM.
type CloudHypervisor struct {
	conf   *config.Config
	store  storage.Store[hypervisor.VMIndex]
	locker lock.Locker
}

// New creates a CloudHypervisor backend.
func New(conf *config.Config) (*CloudHypervisor, error) {
	if err := conf.EnsureCHDirs(); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}
	locker := flock.New(conf.CHIndexLock())
	store := storejson.New[hypervisor.VMIndex](conf.CHIndexFile(), locker)
	return &CloudHypervisor{conf: conf, store: store, locker: locker}, nil
}

func (ch *CloudHypervisor) Type() string { return typ }

// Create, Start, Stop, Inspect, List, Delete — to be implemented.

func (ch *CloudHypervisor) Create(_ context.Context, _ *types.VMConfig, _ []*types.StorageConfig, _ *types.BootConfig) (*types.VMInfo, error) {
	panic("not implemented")
}

func (ch *CloudHypervisor) Start(_ context.Context, _ []string) ([]string, error) {
	panic("not implemented")
}

func (ch *CloudHypervisor) Stop(_ context.Context, _ []string) ([]string, error) {
	panic("not implemented")
}

func (ch *CloudHypervisor) Inspect(_ context.Context, _ string) (*types.VMInfo, error) {
	panic("not implemented")
}

func (ch *CloudHypervisor) List(_ context.Context) ([]*types.VMInfo, error) {
	panic("not implemented")
}

func (ch *CloudHypervisor) Delete(_ context.Context, _ []string) ([]string, error) {
	panic("not implemented")
}

