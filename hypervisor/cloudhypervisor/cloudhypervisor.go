package cloudhypervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/lock/flock"
	"github.com/projecteru2/cocoon/storage"
	storejson "github.com/projecteru2/cocoon/storage/json"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

const typ = "cloud-hypervisor"

// CloudHypervisor implements hypervisor.Hypervisor using the Cloud Hypervisor VMM.
type CloudHypervisor struct {
	conf   *config.Config
	store  storage.Store[hypervisor.VMIndex]
	locker lock.Locker
}

// New creates a CloudHypervisor backend.
func New(conf *config.Config) (*CloudHypervisor, error) {
	if err := conf.EnsureCHDirs(); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}
	locker := flock.New(conf.CHIndexLock())
	store := storejson.New[hypervisor.VMIndex](conf.CHIndexFile(), locker)
	return &CloudHypervisor{conf: conf, store: store, locker: locker}, nil
}

func (ch *CloudHypervisor) Type() string { return typ }

// Inspect returns the VMInfo for a single VM by ref (ID, name, or prefix).
// Returns hypervisor.ErrNotFound if the VM does not exist.
// Runtime fields (PID, SocketPath) are populated from the PID file and config.
func (ch *CloudHypervisor) Inspect(ctx context.Context, ref string) (*types.VMInfo, error) {
	var result *types.VMInfo
	return result, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		id, err := hypervisor.ResolveVMRef(idx, ref)
		if err != nil {
			return err
		}
		info := idx.VMs[id].VMInfo // value copy — detached from the DB record
		ch.enrichRuntime(&info)
		result = &info
		return nil
	})
}

// Console returns the PTY device path for a running VM's virtio-console.
// Resolves ref to an exact ID, verifies the VM is running, then queries
// GET /api/v1/vm.info to obtain the PTY path allocated by Cloud Hypervisor.
func (ch *CloudHypervisor) Console(ctx context.Context, ref string) (string, error) {
	info, err := ch.Inspect(ctx, ref)
	if err != nil {
		return "", err
	}

	pid, _ := utils.ReadPIDFile(ch.conf.CHVMPIDFile(info.ID))
	if !utils.VerifyProcess(pid, filepath.Base(ch.conf.CHBinary)) {
		return "", fmt.Errorf("VM %s is not running", info.ID)
	}

	socketPath := ch.conf.CHVMSocketPath(info.ID)
	body, err := hypervisor.DoGET(ctx, socketPath, "/api/v1/vm.info")
	if err != nil {
		return "", fmt.Errorf("query vm.info: %w", err)
	}

	var vmInfo chVMInfoResponse
	if err := json.Unmarshal(body, &vmInfo); err != nil {
		return "", fmt.Errorf("decode vm.info: %w", err)
	}

	ptyPath := vmInfo.Config.Console.File
	if ptyPath == "" {
		return "", fmt.Errorf("no console PTY allocated for VM %s (mode: %s)", info.ID, vmInfo.Config.Console.Mode)
	}
	return ptyPath, nil
}

// List returns VMInfo for all known VMs.
// Runtime fields are populated for each entry.
func (ch *CloudHypervisor) List(ctx context.Context) ([]*types.VMInfo, error) {
	var result []*types.VMInfo
	return result, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		for _, rec := range idx.VMs {
			if rec == nil {
				continue
			}
			info := rec.VMInfo
			ch.enrichRuntime(&info)
			result = append(result, &info)
		}
		return nil
	})
}

// Delete removes VM records from the index and returns the IDs that were deleted.
// Running VMs are rejected unless force is true, in which case they are stopped first.
// Best-effort: all IDs are attempted; partial results and collected errors are returned.
func (ch *CloudHypervisor) Delete(ctx context.Context, refs []string, force bool) ([]string, error) {
	ids, err := ch.resolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	return forEachVM(ctx, ids, "Delete", true, func(ctx context.Context, id string) error {
		pid, _ := utils.ReadPIDFile(ch.conf.CHVMPIDFile(id))
		if utils.VerifyProcess(pid, filepath.Base(ch.conf.CHBinary)) {
			if !force {
				return fmt.Errorf("running (force required)")
			}
			if err := ch.stopOne(ctx, id); err != nil {
				return fmt.Errorf("stop before delete: %w", err)
			}
		}
		if err := ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
			rec := idx.VMs[id]
			if rec == nil {
				return hypervisor.ErrNotFound
			}
			delete(idx.Names, rec.Config.Name)
			delete(idx.VMs, id)
			return nil
		}); err != nil {
			return err
		}
		ch.removeVMDirs(id)
		return nil
	})
}

// resolveRefs batch-resolves user-supplied references (IDs, names, or prefixes)
// to exact VM IDs under a single lock.
func (ch *CloudHypervisor) resolveRefs(ctx context.Context, refs []string) ([]string, error) {
	var ids []string
	return ids, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		for _, ref := range refs {
			id, err := hypervisor.ResolveVMRef(idx, ref)
			if err != nil {
				return fmt.Errorf("resolve %q: %w", ref, err)
			}
			ids = append(ids, id)
		}
		return nil
	})
}
