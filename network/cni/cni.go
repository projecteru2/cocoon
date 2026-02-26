package cni

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/containernetworking/cni/libcni"
	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/lock/flock"
	"github.com/projecteru2/cocoon/storage"
	storejson "github.com/projecteru2/cocoon/storage/json"
	"github.com/projecteru2/cocoon/types"
)

const typ = "cni"

// CNI implements network.Network using CNI plugins with per-VM netns + bridge + tap.
type CNI struct {
	conf            *config.Config
	store           storage.Store[networkIndex]
	locker          lock.Locker
	networkConfList *libcni.NetworkConfigList
	cniConf         *libcni.CNIConfig
}

// New creates a CNI network provider.
// CNI conflist loading is best-effort at creation time; if no conflist is
// available (e.g. no network needed), Delete/Inspect/List still work.
// Config() will fail if the conflist is not loaded.
func New(conf *config.Config) (*CNI, error) {
	if err := conf.EnsureCNIDirs(); err != nil {
		return nil, fmt.Errorf("ensure cni dirs: %w", err)
	}

	locker := flock.New(conf.CNIIndexLock())
	store := storejson.New[networkIndex](conf.CNIIndexFile(), locker)

	c := &CNI{
		conf:   conf,
		store:  store,
		locker: locker,
	}

	if confList, loadErr := loadFirstConfList(conf.CNIConfDir); loadErr == nil {
		c.networkConfList = confList
		c.cniConf = libcni.NewCNIConfigWithCacheDir(
			[]string{conf.CNIBinDir},
			conf.CNICacheDir(),
			nil,
		)
	}

	return c, nil
}

func (c *CNI) Type() string { return typ }

// Inspect returns the network record for a single network ID.
// Returns (nil, nil) if not found.
func (c *CNI) Inspect(ctx context.Context, id string) (*types.Network, error) {
	var result *types.Network
	return result, c.store.With(ctx, func(idx *networkIndex) error {
		rec := idx.Networks[id]
		if rec == nil {
			return nil
		}
		net := rec.Network // value copy
		result = &net
		return nil
	})
}

// List returns all known network records.
func (c *CNI) List(ctx context.Context) ([]*types.Network, error) {
	var result []*types.Network
	return result, c.store.With(ctx, func(idx *networkIndex) error {
		for _, rec := range idx.Networks {
			if rec == nil {
				continue
			}
			net := rec.Network
			result = append(result, &net)
		}
		return nil
	})
}

// Delete removes all network resources for the given VM IDs:
//  1. CNI DEL for each NIC (releases IP from IPAM, removes veth pair).
//  2. Remove the named netns (kernel cleans up bridge + tap automatically).
//  3. Remove network records from the DB.
//
// Best-effort: failing to clean one VM does not block others.
// Returns the VM IDs that were fully cleaned.
func (c *CNI) Delete(ctx context.Context, vmIDs []string) ([]string, error) {
	var deleted []string
	var errs []error
	for _, vmID := range vmIDs {
		if err := c.deleteVM(ctx, vmID); err != nil {
			errs = append(errs, fmt.Errorf("VM %s: %w", vmID, err))
			continue
		}
		deleted = append(deleted, vmID)
	}
	return deleted, errors.Join(errs...)
}

// deleteVM cleans up all network resources for a single VM.
func (c *CNI) deleteVM(ctx context.Context, vmID string) error {
	logger := log.WithFunc("cni.deleteVM")

	// Collect value-copies of records for this VM.
	var records []networkRecord
	if err := c.store.With(ctx, func(idx *networkIndex) error {
		records = idx.byVMID(vmID)
		return nil
	}); err != nil {
		return fmt.Errorf("read network index: %w", err)
	}

	// Nothing to clean — VM had no network or was already cleaned.
	if len(records) == 0 {
		return nil
	}

	nsPath := c.conf.CNINetnsPath(vmID)

	// CNI DEL for each NIC — releases IPs from IPAM and removes veth pairs.
	// Best-effort: log failures but continue. Netns deletion cleans up
	// devices anyway; CNI DEL is primarily for IPAM bookkeeping.
	if c.cniConf != nil && c.networkConfList != nil {
		for _, rec := range records {
			rt := &libcni.RuntimeConf{
				ContainerID: vmID,
				NetNS:       nsPath,
				IfName:      rec.IfName,
			}
			if err := c.cniConf.DelNetworkList(ctx, c.networkConfList, rt); err != nil {
				logger.Warnf(ctx, "CNI DEL %s/%s: %v (continuing)", vmID, rec.IfName, err)
			}
		}
	}

	// Remove the named netns (unmount bind-mount + remove file).
	// deleteNetns retries briefly to handle async fd cleanup after process kill.
	nsName := c.conf.CNINetnsName(vmID)
	if err := deleteNetns(nsName); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove netns %s: %w", nsPath, err)
	}

	// Remove records from DB.
	return c.store.Update(ctx, func(idx *networkIndex) error {
		for id, rec := range idx.Networks {
			if rec != nil && rec.VMID == vmID {
				delete(idx.Networks, id)
			}
		}
		return nil
	})
}

func loadFirstConfList(dir string) (*libcni.NetworkConfigList, error) {
	files, err := libcni.ConfFiles(dir, []string{".conflist"})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .conflist files in %s", dir)
	}
	// files are already sorted by ConfFiles.
	return libcni.ConfListFromFile(files[0])
}
