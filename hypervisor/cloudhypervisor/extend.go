package cloudhypervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/cocoonstack/cocoon/extend/fs"
	"github.com/cocoonstack/cocoon/extend/vfio"
	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

var (
	_ fs.Attacher   = (*CloudHypervisor)(nil)
	_ fs.Lister     = (*CloudHypervisor)(nil)
	_ vfio.Attacher = (*CloudHypervisor)(nil)
	_ vfio.Lister   = (*CloudHypervisor)(nil)
)

// FsAttach hot-plugs a vhost-user-fs device onto a running CH VM.
func (ch *CloudHypervisor) FsAttach(ctx context.Context, vmRef string, spec fs.Spec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	hc, err := ch.runningVMClient(ctx, vmRef)
	if err != nil {
		return "", err
	}
	info, err := getVMInfo(ctx, hc)
	if err != nil {
		return "", err
	}
	if !info.Config.Memory.Shared {
		return "", fmt.Errorf("fs attach requires VM memory shared=on; recreate the VM with --shared-memory")
	}
	id := fs.DeriveID(spec.Tag)
	for _, existing := range info.Config.Fs {
		if existing.Tag == spec.Tag {
			return "", fmt.Errorf("fs tag %q already attached", spec.Tag)
		}
		if existing.ID == id {
			return "", fmt.Errorf("fs id %q already attached", id)
		}
	}
	resp, err := addFsVM(ctx, hc, chFs{
		ID:        id,
		Tag:       spec.Tag,
		Socket:    spec.Socket,
		NumQueues: spec.NumQueues,
		QueueSize: spec.QueueSize,
	})
	if err != nil {
		return "", fmt.Errorf("vm.add-fs: %w", err)
	}
	if resp.ID != "" {
		return resp.ID, nil
	}
	return id, nil
}

// FsDetach removes a previously attached vhost-user-fs device by tag.
func (ch *CloudHypervisor) FsDetach(ctx context.Context, vmRef, tag string) error {
	if tag == "" {
		return fmt.Errorf("--tag is required")
	}
	hc, err := ch.runningVMClient(ctx, vmRef)
	if err != nil {
		return err
	}
	info, err := getVMInfo(ctx, hc)
	if err != nil {
		return err
	}
	deviceID := ""
	for _, existing := range info.Config.Fs {
		if existing.Tag == tag {
			deviceID = existing.ID
			break
		}
	}
	if deviceID == "" {
		return fmt.Errorf("fs tag %q not attached", tag)
	}
	if err := removeDeviceVM(ctx, hc, deviceID); err != nil {
		return fmt.Errorf("vm.remove-device %s: %w", deviceID, err)
	}
	return nil
}

// FsList enumerates currently attached vhost-user-fs devices.
func (ch *CloudHypervisor) FsList(ctx context.Context, vmRef string) ([]fs.Attached, error) {
	hc, err := ch.runningVMClient(ctx, vmRef)
	if err != nil {
		if errors.Is(err, hypervisor.ErrNotRunning) {
			return nil, nil
		}
		return nil, err
	}
	info, err := getVMInfo(ctx, hc)
	if err != nil {
		return nil, err
	}
	out := make([]fs.Attached, 0, len(info.Config.Fs))
	for _, f := range info.Config.Fs {
		out = append(out, fs.Attached{ID: f.ID, Tag: f.Tag, Socket: f.Socket})
	}
	return out, nil
}

// DeviceAttach hot-plugs a VFIO PCI passthrough device onto a running CH VM.
func (ch *CloudHypervisor) DeviceAttach(ctx context.Context, vmRef string, spec vfio.Spec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	path, err := vfio.NormalizePath(spec.PCI)
	if err != nil {
		return "", err
	}
	if st, statErr := os.Stat(path); statErr != nil {
		return "", fmt.Errorf("pci path %s: %w", path, statErr)
	} else if !st.IsDir() {
		return "", fmt.Errorf("pci path %s: not a directory", path)
	}
	hc, err := ch.runningVMClient(ctx, vmRef)
	if err != nil {
		return "", err
	}
	info, err := getVMInfo(ctx, hc)
	if err != nil {
		return "", err
	}
	for _, existing := range info.Config.Devices {
		if existing.Path == path {
			return "", fmt.Errorf("device %s already attached (id=%s)", path, existing.ID)
		}
		if spec.ID != "" && existing.ID == spec.ID {
			return "", fmt.Errorf("device id %q already in use", spec.ID)
		}
	}
	resp, err := addDeviceVM(ctx, hc, chDevice{ID: spec.ID, Path: path})
	if err != nil {
		return "", fmt.Errorf("vm.add-device: %w", err)
	}
	if resp.ID != "" {
		return resp.ID, nil
	}
	return spec.ID, nil
}

// DeviceDetach removes a previously attached VFIO device by id.
func (ch *CloudHypervisor) DeviceDetach(ctx context.Context, vmRef, id string) error {
	if id == "" {
		return fmt.Errorf("--id is required")
	}
	hc, err := ch.runningVMClient(ctx, vmRef)
	if err != nil {
		return err
	}
	info, err := getVMInfo(ctx, hc)
	if err != nil {
		return err
	}
	found := false
	for _, existing := range info.Config.Devices {
		if existing.ID == id {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("device id %q not attached", id)
	}
	if err := removeDeviceVM(ctx, hc, id); err != nil {
		return fmt.Errorf("vm.remove-device %s: %w", id, err)
	}
	return nil
}

// DeviceList enumerates currently attached VFIO PCI passthrough devices.
func (ch *CloudHypervisor) DeviceList(ctx context.Context, vmRef string) ([]vfio.Attached, error) {
	hc, err := ch.runningVMClient(ctx, vmRef)
	if err != nil {
		if errors.Is(err, hypervisor.ErrNotRunning) {
			return nil, nil
		}
		return nil, err
	}
	info, err := getVMInfo(ctx, hc)
	if err != nil {
		return nil, err
	}
	out := make([]vfio.Attached, 0, len(info.Config.Devices))
	for _, d := range info.Config.Devices {
		out = append(out, vfio.Attached{ID: d.ID, BDF: bdfFromSysfsPath(d.Path)})
	}
	return out, nil
}

// runningVMClient resolves vmRef, asserts the VM is in Running state, and
// returns an http.Client connected to its CH API socket.
func (ch *CloudHypervisor) runningVMClient(ctx context.Context, vmRef string) (*http.Client, error) {
	vmID, err := ch.ResolveRef(ctx, vmRef)
	if err != nil {
		return nil, err
	}
	rec, err := ch.LoadRecord(ctx, vmID)
	if err != nil {
		return nil, err
	}
	if rec.State != types.VMStateRunning {
		return nil, fmt.Errorf("vm %s is %s: %w", vmID, rec.State, hypervisor.ErrNotRunning)
	}
	if !utils.IsProcessAlive(rec.PID) {
		return nil, fmt.Errorf("vm %s pid %d not alive: %w", vmID, rec.PID, hypervisor.ErrNotRunning)
	}
	return utils.NewSocketHTTPClient(hypervisor.SocketPath(rec.RunDir)), nil
}

// bdfFromSysfsPath strips /sys/bus/pci/devices/ prefix when present.
func bdfFromSysfsPath(p string) string {
	return strings.TrimPrefix(p, vfio.SysfsPCIPrefix)
}
