package cloudhypervisor

import (
	"context"
	"fmt"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/lock/flock"
	storejson "github.com/cocoonstack/cocoon/storage/json"
)

const typ = "cloud-hypervisor"

// compile-time interface checks.
var (
	_ hypervisor.Hypervisor = (*CloudHypervisor)(nil)
	_ hypervisor.Direct     = (*CloudHypervisor)(nil)
	_ hypervisor.Watchable  = (*CloudHypervisor)(nil)
)

// CloudHypervisor implements hypervisor.Hypervisor.
type CloudHypervisor struct {
	*hypervisor.Backend
	conf *Config
}

// New creates a CloudHypervisor backend.
func New(conf *config.Config) (*CloudHypervisor, error) {
	if conf == nil {
		return nil, fmt.Errorf("config is nil")
	}
	cfg := NewConfig(conf)
	if err := cfg.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}
	locker := flock.New(cfg.IndexLock())
	store := storejson.New[hypervisor.VMIndex](cfg.IndexFile(), locker)
	return &CloudHypervisor{
		Backend: &hypervisor.Backend{
			Typ:    typ,
			Conf:   cfg,
			DB:     store,
			Locker: locker,
		},
		conf: cfg,
	}, nil
}

// Delete removes VMs. Running VMs require force=true (stops them first).
func (ch *CloudHypervisor) Delete(ctx context.Context, refs []string, force bool) ([]string, error) {
	return ch.DeleteAll(ctx, refs, force, ch.stopOne)
}
