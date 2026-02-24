package images

import (
	"context"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/progress"
	"github.com/projecteru2/cocoon/types"
)

type Images interface {
	Type() string

	Pull(context.Context, string, progress.Tracker) error
	List(context.Context) ([]*types.Image, error)
	Delete(context.Context, []string) ([]string, error)
	RegisterGC(*gc.Orchestrator)

	Config(context.Context, []*types.VMConfig) ([][]*types.StorageConfig, []*types.BootConfig, error)
}
