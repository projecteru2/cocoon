package cloudhypervisor

import (
	"context"
	"encoding/json"
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
	id := fs.DeriveID(spec.Tag)
	return ch.attachWith(ctx, vmRef, "vm.add-fs", chFs{
		ID:        id,
		Tag:       spec.Tag,
		Socket:    spec.Socket,
		NumQueues: spec.NumQueues,
		QueueSize: spec.QueueSize,
	}, id, func(info *chVMInfoResponse) error {
		if !info.Config.Memory.Shared {
			return fmt.Errorf("fs attach requires the VM to be created with --shared-memory (current memory shared=off; cannot be flipped on a running VM)")
		}
		for _, ex := range info.Config.Fs {
			if ex.Tag == spec.Tag {
				return fmt.Errorf("fs tag %q already attached", spec.Tag)
			}
			if ex.ID == id {
				return fmt.Errorf("fs id %q already attached", id)
			}
		}
		return nil
	})
}

// FsDetach removes a previously attached vhost-user-fs device by tag.
func (ch *CloudHypervisor) FsDetach(ctx context.Context, vmRef, tag string) error {
	if tag == "" {
		return fmt.Errorf("tag is required")
	}
	return ch.detachWith(ctx, vmRef, func(info *chVMInfoResponse) (string, error) {
		for _, ex := range info.Config.Fs {
			if ex.Tag == tag {
				return ex.ID, nil
			}
		}
		return "", fmt.Errorf("fs tag %q not attached", tag)
	})
}

// FsList enumerates currently attached vhost-user-fs devices.
//
// TODO(inspect): cmd/vm Inspect calls FsList and DeviceList back-to-back,
// each fetching its own vm.info. A combined Lister returning both arrays
// from a single vm.info call would halve the round-trips on a running VM.
func (ch *CloudHypervisor) FsList(ctx context.Context, vmRef string) ([]fs.Attached, error) {
	return listWith(ctx, ch, vmRef, func(info *chVMInfoResponse) []fs.Attached {
		out := make([]fs.Attached, 0, len(info.Config.Fs))
		for _, f := range info.Config.Fs {
			out = append(out, fs.Attached{ID: f.ID, Tag: f.Tag, Socket: f.Socket})
		}
		return out
	})
}

// DeviceAttach hot-plugs a VFIO PCI passthrough device onto a running CH VM.
func (ch *CloudHypervisor) DeviceAttach(ctx context.Context, vmRef string, spec vfio.Spec) (string, error) {
	path, err := spec.NormalizedPath()
	if err != nil {
		return "", err
	}
	return ch.attachWith(ctx, vmRef, "vm.add-device", chDevice{
		ID:   spec.ID,
		Path: path,
	}, spec.ID, func(info *chVMInfoResponse) error {
		// host stat happens after the running-VM gate inside attachWith,
		// so a stopped VM reports the VM-state error instead of misleading
		// host path output.
		st, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("pci path %s: %w", path, statErr)
		}
		if !st.IsDir() {
			return fmt.Errorf("pci path %s: not a directory", path)
		}
		for _, ex := range info.Config.Devices {
			if ex.Path == path {
				return fmt.Errorf("device %s already attached (id=%s)", path, ex.ID)
			}
			if spec.ID != "" && ex.ID == spec.ID {
				return fmt.Errorf("device id %q already in use", spec.ID)
			}
		}
		return nil
	})
}

// DeviceDetach removes a previously attached VFIO device by id.
func (ch *CloudHypervisor) DeviceDetach(ctx context.Context, vmRef, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	return ch.detachWith(ctx, vmRef, func(info *chVMInfoResponse) (string, error) {
		for _, ex := range info.Config.Devices {
			if ex.ID == id {
				return id, nil
			}
		}
		return "", fmt.Errorf("device id %q not attached", id)
	})
}

// DeviceList enumerates currently attached VFIO PCI passthrough devices.
//
// TODO(inspect): see FsList note — combined Lister would dedupe vm.info.
func (ch *CloudHypervisor) DeviceList(ctx context.Context, vmRef string) ([]vfio.Attached, error) {
	return listWith(ctx, ch, vmRef, func(info *chVMInfoResponse) []vfio.Attached {
		out := make([]vfio.Attached, 0, len(info.Config.Devices))
		for _, d := range info.Config.Devices {
			out = append(out, vfio.Attached{ID: d.ID, BDF: bdfFromSysfsPath(d.Path)})
		}
		return out
	})
}

// inspectRunning is the shared bootstrap for every extend op: gate on a
// live VM and grab a fresh vm.info snapshot to feed conflict scans,
// memory checks, or device-id lookups.
func (ch *CloudHypervisor) inspectRunning(ctx context.Context, vmRef string) (*http.Client, *chVMInfoResponse, error) {
	hc, err := ch.runningVMClient(ctx, vmRef)
	if err != nil {
		return nil, nil, err
	}
	info, err := getVMInfo(ctx, hc)
	if err != nil {
		return nil, nil, err
	}
	return hc, info, nil
}

// attachWith is the shared skeleton for hot-add operations.
func (ch *CloudHypervisor) attachWith(
	ctx context.Context, vmRef, endpoint string,
	body any, fallbackID string,
	preCheck func(*chVMInfoResponse) error,
) (string, error) {
	hc, info, err := ch.inspectRunning(ctx, vmRef)
	if err != nil {
		return "", err
	}
	if checkErr := preCheck(info); checkErr != nil {
		return "", checkErr
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal %s: %w", endpoint, err)
	}
	// vm.add-fs / vm.add-device are not idempotent: a retry after CH already
	// accepted the device but the response was lost would echo back as a
	// misleading "duplicate id" rejection. vmAPIOnce skips the retry layer.
	resp, err := vmAPIOnce(ctx, hc, endpoint, bodyBytes, http.StatusOK, http.StatusNoContent)
	if err != nil {
		return "", fmt.Errorf("%s: %w", endpoint, err)
	}
	pci, err := decodePciDeviceInfo(resp)
	if err != nil {
		return "", err
	}
	if pci.ID != "" {
		return pci.ID, nil
	}
	return fallbackID, nil
}

// detachWith is the shared skeleton for hot-remove operations.
func (ch *CloudHypervisor) detachWith(
	ctx context.Context, vmRef string,
	findID func(*chVMInfoResponse) (string, error),
) error {
	hc, info, err := ch.inspectRunning(ctx, vmRef)
	if err != nil {
		return err
	}
	deviceID, err := findID(info)
	if err != nil {
		return err
	}
	if err := removeDeviceVM(ctx, hc, deviceID); err != nil {
		return fmt.Errorf("vm.remove-device %s: %w", deviceID, err)
	}
	return nil
}

// listWith is the shared skeleton for inspect-time enumeration.
// Stopped VMs return a nil slice (not an error) so inspect can omit the
// field cleanly.
func listWith[A any](
	ctx context.Context, ch *CloudHypervisor, vmRef string,
	extract func(*chVMInfoResponse) []A,
) ([]A, error) {
	_, info, err := ch.inspectRunning(ctx, vmRef)
	if err != nil {
		if errors.Is(err, hypervisor.ErrNotRunning) {
			return nil, nil
		}
		return nil, err
	}
	return extract(info), nil
}

// runningVMClient resolves vmRef, asserts the recorded CH process is still
// alive (PID file + cmdline match — same gate as Backend.WithRunningVM),
// and returns an http.Client connected to its CH API socket.
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
	sockPath := hypervisor.SocketPath(rec.RunDir)
	pid, _ := utils.ReadPIDFile(ch.PIDFilePath(rec.RunDir))
	if !utils.VerifyProcessCmdline(pid, ch.conf.BinaryName(), sockPath) {
		return nil, fmt.Errorf("vm %s pid %d not %s: %w", vmID, pid, ch.conf.BinaryName(), hypervisor.ErrNotRunning)
	}
	return utils.NewSocketHTTPClient(sockPath), nil
}

// bdfFromSysfsPath returns the BDF suffix when path is under the canonical
// sysfs PCI prefix; empty otherwise (CH may report a non-PCI host path).
func bdfFromSysfsPath(p string) string {
	if !strings.HasPrefix(p, vfio.SysfsPCIPrefix) {
		return ""
	}
	return strings.TrimPrefix(p, vfio.SysfsPCIPrefix)
}
