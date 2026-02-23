package main

import (
	"context"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/storage/oci"
	coretypes "github.com/projecteru2/core/types"
)

func main() {
	ctx := context.Background()
	conf := &config.Config{
		RootDir:  "./tmp",
		PoolSize: 10,
		Log: coretypes.ServerLogConfig{
			Level: "debug",
		},
	}
	oci, err := oci.New(ctx, conf)
	if err != nil {
		panic(err)
	}
	defer oci.Close()

	if err := oci.Pull(ctx, "ghcr.io/projecteru2/cocoon/ubuntu:24.04"); err != nil {
		panic(err)
	}

}
