package images

import (
	"context"

	"github.com/cocoonstack/cocoon/gc"
	"github.com/cocoonstack/cocoon/progress"
	"github.com/cocoonstack/cocoon/types"
)

type Images interface {
	Type() string

	Pull(context.Context, string, progress.Tracker) error
	Import(ctx context.Context, name string, tracker progress.Tracker, file ...string) error
	Inspect(context.Context, string) (*types.Image, error)
	List(context.Context) ([]*types.Image, error)
	Delete(context.Context, []string) ([]string, error)
	RegisterGC(*gc.Orchestrator)

	Config(context.Context, []*types.VMConfig) ([][]*types.StorageConfig, []*types.BootConfig, error)
}
