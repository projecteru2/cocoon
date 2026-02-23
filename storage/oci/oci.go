package oci

import (
	"context"

	"github.com/projecteru2/cocoon/config"
)

type OCI struct {
	conf *config.Config
}

func New(conf *config.Config) *OCI {
	return &OCI{
		conf: conf,
	}
}

func Pull(ctx context.Context, image string) error {
	return nil
}
