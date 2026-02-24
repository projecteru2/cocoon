package cmd

import (
	"context"
	"fmt"
	"strings"

	units "github.com/docker/go-units"

	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/hypervisor/cloudhypervisor"
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/images/cloudimg"
	"github.com/projecteru2/cocoon/images/oci"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// initBackends initializes all image backends and the hypervisor.
func initBackends(ctx context.Context) ([]images.Images, hypervisor.Hypervisor, error) {
	ociStore, err := oci.New(ctx, conf)
	if err != nil {
		return nil, nil, fmt.Errorf("init oci backend: %w", err)
	}
	cloudimgStore, err := cloudimg.New(ctx, conf)
	if err != nil {
		return nil, nil, fmt.Errorf("init cloudimg backend: %w", err)
	}
	ch, err := cloudhypervisor.New(conf)
	if err != nil {
		return nil, nil, fmt.Errorf("init hypervisor: %w", err)
	}
	backends := []images.Images{ociStore, cloudimgStore}
	return backends, ch, nil
}

// initImageBackends initializes only image backends (no hypervisor needed).
func initImageBackends(ctx context.Context) ([]images.Images, *oci.OCI, *cloudimg.CloudImg, error) {
	ociStore, err := oci.New(ctx, conf)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("init oci backend: %w", err)
	}
	cloudimgStore, err := cloudimg.New(ctx, conf)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("init cloudimg backend: %w", err)
	}
	return []images.Images{ociStore, cloudimgStore}, ociStore, cloudimgStore, nil
}

// initHypervisor initializes only the hypervisor.
func initHypervisor() (hypervisor.Hypervisor, error) {
	ch, err := cloudhypervisor.New(conf)
	if err != nil {
		return nil, fmt.Errorf("init hypervisor: %w", err)
	}
	return ch, nil
}

// resolveImage resolves an image reference to StorageConfigs + BootConfig via image backends.
func resolveImage(ctx context.Context, backends []images.Images, vmCfg *types.VMConfig) ([]*types.StorageConfig, *types.BootConfig, error) {
	vms := []*types.VMConfig{vmCfg}
	var storageConfigs []*types.StorageConfig
	var bootCfg *types.BootConfig
	var backendErrs []string
	for _, b := range backends {
		confs, boots, err := b.Config(ctx, vms)
		if err != nil {
			backendErrs = append(backendErrs, fmt.Sprintf("%s: %v", b.Type(), err))
			continue
		}
		storageConfigs = confs[0]
		bootCfg = boots[0]
		break
	}
	if bootCfg == nil {
		return nil, nil, fmt.Errorf("image %q not resolved: %s", vmCfg.Image, strings.Join(backendErrs, "; "))
	}
	return storageConfigs, bootCfg, nil
}

func formatSize(bytes int64) string {
	return units.HumanSize(float64(bytes))
}

func isURL(ref string) bool {
	return strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
}

// reconcileState checks actual process liveness to detect stale "running" records.
func reconcileState(vm *types.VMInfo) string {
	if vm.State == types.VMStateRunning && !utils.IsProcessAlive(vm.PID) {
		return "stopped (stale)"
	}
	return string(vm.State)
}
