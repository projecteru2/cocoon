package daemon

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/projecteru2/cocoon/gc"
	"github.com/projecteru2/cocoon/progress"
	"github.com/projecteru2/cocoon/types"
)

// stubHypervisor implements hypervisor.Hypervisor with minimal behavior.
type stubHypervisor struct{}

func (s *stubHypervisor) Type() string { return "stub" }

func (s *stubHypervisor) Create(_ context.Context, vmID string, vmCfg *types.VMConfig, _ []*types.StorageConfig, _ []*types.NetworkConfig, _ *types.BootConfig) (*types.VM, error) {
	return &types.VM{ID: vmID, State: types.VMStateCreated, Config: *vmCfg, CreatedAt: time.Now()}, nil
}

func (s *stubHypervisor) Start(_ context.Context, refs []string) ([]string, error) {
	return refs, nil
}

func (s *stubHypervisor) Stop(_ context.Context, refs []string) ([]string, error) {
	return refs, nil
}

func (s *stubHypervisor) Inspect(_ context.Context, ref string) (*types.VM, error) {
	return nil, fmt.Errorf("VM %s not found", ref)
}

func (s *stubHypervisor) List(_ context.Context) ([]*types.VM, error) {
	return nil, nil
}

func (s *stubHypervisor) Delete(_ context.Context, refs []string, _ bool) ([]string, error) {
	return refs, nil
}

func (s *stubHypervisor) Console(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("not supported")
}

func (s *stubHypervisor) Snapshot(_ context.Context, _ string) (*types.SnapshotConfig, io.ReadCloser, error) {
	return nil, nil, fmt.Errorf("not supported")
}

func (s *stubHypervisor) Clone(_ context.Context, vmID string, vmCfg *types.VMConfig, _ []*types.NetworkConfig, _ *types.SnapshotConfig, _ io.Reader) (*types.VM, error) {
	return &types.VM{ID: vmID, State: types.VMStateRunning, Config: *vmCfg}, nil
}

func (s *stubHypervisor) Restore(_ context.Context, vmRef string, vmCfg *types.VMConfig, _ io.Reader) (*types.VM, error) {
	return &types.VM{ID: vmRef, State: types.VMStateRunning, Config: *vmCfg}, nil
}

func (s *stubHypervisor) RegisterGC(_ *gc.Orchestrator) {}

// stubImages implements images.Images with minimal behavior.
type stubImages struct{}

func (s *stubImages) Type() string                                               { return "stub-images" }
func (s *stubImages) Pull(_ context.Context, _ string, _ progress.Tracker) error { return nil }
func (s *stubImages) Import(_ context.Context, _ string, _ progress.Tracker, _ ...string) error {
	return nil
}

func (s *stubImages) Inspect(_ context.Context, _ string) (*types.Image, error) { return nil, nil }
func (s *stubImages) List(_ context.Context) ([]*types.Image, error)            { return nil, nil }
func (s *stubImages) Delete(_ context.Context, refs []string) ([]string, error) { return refs, nil }

func (s *stubImages) Config(_ context.Context, vms []*types.VMConfig) ([][]*types.StorageConfig, []*types.BootConfig, error) {
	storages := make([][]*types.StorageConfig, len(vms))
	boots := make([]*types.BootConfig, len(vms))
	for i := range vms {
		storages[i] = []*types.StorageConfig{{Path: "/fake/disk.img", RO: true}}
		boots[i] = &types.BootConfig{KernelPath: "/fake/vmlinuz"}
	}
	return storages, boots, nil
}

func (s *stubImages) RegisterGC(_ *gc.Orchestrator) {}
