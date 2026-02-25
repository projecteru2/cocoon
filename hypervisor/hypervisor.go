package hypervisor

import (
	"context"
	"errors"
	"io"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/types"
)

var (
	ErrNotFound   = errors.New("VM not found")
	ErrNotRunning = errors.New("VM not running")
)

// Hypervisor manages VM lifecycle. Implemented by each backend.
type Hypervisor interface {
	Type() string

	Create(context.Context, *types.VMConfig, []*types.StorageConfig, *types.BootConfig) (*types.VMInfo, error)
	Start(ctx context.Context, refs []string) ([]string, error)
	Stop(ctx context.Context, refs []string) ([]string, error)
	Inspect(ctx context.Context, ref string) (*types.VMInfo, error)
	List(context.Context) ([]*types.VMInfo, error)
	Delete(ctx context.Context, refs []string, force bool) ([]string, error)
	Console(ctx context.Context, ref string) (io.ReadCloser, error)

	// TODO SNAPSHOT
	// TODO RESTORE
	// TODO MIGRATE
	RegisterGC(*gc.Orchestrator)
}
