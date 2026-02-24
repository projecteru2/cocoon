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
	Start(context.Context, []string) ([]string, error)
	Stop(context.Context, []string) ([]string, error)
	Inspect(context.Context, string) (*types.VMInfo, error)
	List(context.Context) ([]*types.VMInfo, error)
	Delete(ctx context.Context, ids []string, force bool) ([]string, error)
	// TODO Console
	// TODO SNAPSHOT
	// TODO RESTORE
	// TODO MIGRTE
	RegisterGC(*gc.Orchestrator)
}
