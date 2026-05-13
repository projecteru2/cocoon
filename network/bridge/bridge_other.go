//go:build !linux

package bridge

import (
	"context"
	"fmt"
	"runtime"

	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/gc"
	"github.com/cocoonstack/cocoon/network"
	"github.com/cocoonstack/cocoon/types"
)

var errUnsupported = fmt.Errorf("bridge TAP networking requires Linux (running on %s)", runtime.GOOS)

// Bridge is a placeholder for non-Linux.
type Bridge struct{}

// New returns an error on non-Linux.
func New(_ *config.Config, _ string) (*Bridge, error) {
	return nil, errUnsupported
}

// Type returns the provider identifier.
func (b *Bridge) Type() string { return "bridge" }

// Verify is not supported.
func (b *Bridge) Verify(_ context.Context, _ string) error { return errUnsupported }

// Prepare is not supported.
func (b *Bridge) Prepare(_ context.Context, _ string, _ *types.VMConfig) (string, error) {
	return "", errUnsupported
}

// Add is not supported.
func (b *Bridge) Add(_ context.Context, _ string, _ *types.VMConfig, _ ...network.AddSpec) ([]*types.NetworkConfig, error) {
	return nil, errUnsupported
}

// Remove is not supported.
func (b *Bridge) Remove(_ context.Context, _ string, _ ...int) error { return errUnsupported }

// Delete is not supported.
func (b *Bridge) Delete(_ context.Context, _ []string) ([]string, error) { return nil, errUnsupported }

// Inspect is not supported.
func (b *Bridge) Inspect(_ context.Context, _ string) (*types.Network, error) {
	return nil, errUnsupported
}

// List is not supported.
func (b *Bridge) List(_ context.Context) ([]*types.Network, error) { return nil, errUnsupported }

// RegisterGC is a no-op.
func (b *Bridge) RegisterGC(_ *gc.Orchestrator) {}

// CleanupTAPs is a no-op on non-Linux.
func CleanupTAPs(_ []string) []string { return nil }
