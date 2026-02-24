package hypervisor

import (
	"context"
	"errors"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/types"
)

// ErrNotFound is returned when a VM ID does not exist in the index.
var ErrNotFound = errors.New("VM not found")

// Hypervisor manages the lifecycle of Cloud Hypervisor processes.
// Each backend (e.g. cloud-hypervisor) implements this interface.
type Hypervisor interface {
	Type() string

	Create(context.Context, *types.VMConfig, []*types.StorageConfig, *types.BootConfig) (*types.VMInfo, error)
	Start(ctx context.Context, refs []string) ([]string, error)
	Stop(ctx context.Context, refs []string) ([]string, error)
	Inspect(ctx context.Context, ref string) (*types.VMInfo, error)
	List(context.Context) ([]*types.VMInfo, error)
	Delete(ctx context.Context, refs []string, force bool) ([]string, error)
	Console(ctx context.Context, ref string) (string, error)

	// TODO SNAPSHOT
	// TODO RESTORE
	// TODO MIGRATE
	RegisterGC(*gc.Orchestrator)
}
