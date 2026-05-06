package firecracker

import (
	"context"
	"fmt"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/lock/flock"
	storejson "github.com/cocoonstack/cocoon/storage/json"
)

const typ = "firecracker"

// compile-time interface checks.
var (
	_ hypervisor.Hypervisor = (*Firecracker)(nil)
	_ hypervisor.Watchable  = (*Firecracker)(nil)
	_ hypervisor.Direct     = (*Firecracker)(nil)
)

// Firecracker implements hypervisor.Hypervisor using the Firecracker VMM.
// Only OCI images (direct kernel boot) are supported — no UEFI, no cloudimg, no Windows.
type Firecracker struct {
	*hypervisor.Backend
	conf *Config
}

// New creates a Firecracker backend.
func New(conf *config.Config) (*Firecracker, error) {
	if conf == nil {
		return nil, fmt.Errorf("config is nil")
	}
	cfg := NewConfig(conf)
	if err := cfg.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}
	locker := flock.New(cfg.IndexLock())
	store := storejson.New[hypervisor.VMIndex](cfg.IndexFile(), locker)
	return &Firecracker{
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
func (fc *Firecracker) Delete(ctx context.Context, refs []string, force bool) ([]string, error) {
	return fc.DeleteAll(ctx, refs, force, fc.stopOne)
}
