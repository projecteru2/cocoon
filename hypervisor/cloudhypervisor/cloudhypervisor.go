package cloudhypervisor

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/hypervisor"
	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/lock/flock"
	"github.com/projecteru2/cocoon/storage"
	storejson "github.com/projecteru2/cocoon/storage/json"
	"github.com/projecteru2/cocoon/types"
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

// Inspect returns the VMInfo for a single VM by ID.
// Returns (nil, nil) if the VM is not found.
// Runtime fields (PID, SocketPath) are populated from the PID file and config.
func (ch *CloudHypervisor) Inspect(ctx context.Context, id string) (*types.VMInfo, error) {
	var result *types.VMInfo
	return result, ch.store.With(ctx, func(idx *hypervisor.VMIndex) error {
		rec := idx.VMs[id]
		if rec == nil {
			return nil
		}
		info := rec.VMInfo // value copy — detached from the DB record
		ch.enrichRuntime(&info)
		result = &info
		return nil
	})
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
// Unknown IDs are silently skipped. Runtime and log directories are left for GC.
func (ch *CloudHypervisor) Delete(ctx context.Context, ids []string) ([]string, error) {
	var deleted []string
	return deleted, ch.store.Update(ctx, func(idx *hypervisor.VMIndex) error {
		for _, id := range ids {
			if _, ok := idx.VMs[id]; ok {
				delete(idx.VMs, id)
				deleted = append(deleted, id)
			}
		}
		return nil
	})
}

// Create, Start, Stop — to be implemented.

func (ch *CloudHypervisor) Create(_ context.Context, _ *types.VMConfig, _ []*types.StorageConfig, _ *types.BootConfig) (*types.VMInfo, error) {
	panic("not implemented")
}

func (ch *CloudHypervisor) Start(_ context.Context, _ []string) ([]string, error) {
	panic("not implemented")
}

func (ch *CloudHypervisor) Stop(_ context.Context, _ []string) ([]string, error) {
	panic("not implemented")
}

// enrichRuntime populates the runtime-only fields of info from live sources:
//   - SocketPath is always derived from config (deterministic).
//   - PID is read from the PID file; 0 means the VM is not running.
func (ch *CloudHypervisor) enrichRuntime(info *types.VMInfo) {
	info.SocketPath = ch.conf.CHVMSocketPath(info.ID)
	info.PID = readPID(ch.conf.CHVMPIDFile(info.ID))
}

// readPID reads a process ID from path.
// Returns 0 if the file does not exist or cannot be parsed.
func readPID(path string) int {
	data, err := os.ReadFile(path) //nolint:gosec // internal runtime path
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}
