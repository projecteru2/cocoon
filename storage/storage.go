package storage

import (
	"context"

	"github.com/projecteru2/cocoon/types"
)

type Storage interface {
	Pull(context.Context, string) error
	List(context.Context) ([]*types.Storage, error)
	Delete(context.Context, string) error

    Config(context.Context, string) (*types.Storage, error)
}
