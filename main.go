package main

import (
	"context"
	"errors"
	"os"

	"github.com/cocoonstack/cocoon/cmd"
	cmdvm "github.com/cocoonstack/cocoon/cmd/vm"
	"github.com/cocoonstack/cocoon/hypervisor/firecracker"
)

func main() {
	ctx := context.Background()
	// Internal: console relay mode for Firecracker PTY bridge.
	// Started as a background process by FC launchProcess.
	if firecracker.IsRelayMode() {
		firecracker.RunRelay(ctx)
		return
	}
	if err := cmd.Execute(ctx); err != nil {
		var exitErr *cmdvm.ExecExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		os.Exit(1)
	}
}
