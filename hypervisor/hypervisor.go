package hypervisor

import (
	"context"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/types"
)

// Hypervisor manages the lifecycle of Cloud Hypervisor processes.
// Each backend (e.g. cloud-hypervisor) implements this interface.
type Hypervisor interface {
	Type() string

	Create(context.Context, *types.VMConfig, []*types.StorageConfig, *types.BootConfig) (*types.VMInfo, error)
	Start(context.Context, []string) ([]string, error)
	Stop(context.Context, []string) ([]string, error)
	Inspect(context.Context, string) (*types.VMInfo, error)
	List(context.Context) ([]*types.VMInfo, error)
	Delete(context.Context, []string) ([]string, error)

	RegisterGC(*gc.Orchestrator)
}
