package cni

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/containernetworking/cni/libcni"
	"github.com/projecteru2/core/log"

	"github.com/projecteru2/cocoon/gc"
)

type cniSnapshot struct {
	dbVMIDs    map[string]struct{} // unique VM IDs from CNI DB records
	netnsNames []string            // VM IDs extracted from /var/run/netns/cocoon-*
}

// GCModule returns the GC module for orphan netns and stale CNI record cleanup.
func (c *CNI) GCModule() gc.Module[cniSnapshot] {
	return gc.Module[cniSnapshot]{
		Name:   typ,
		Locker: c.locker,
		ReadDB: func(_ context.Context) (cniSnapshot, error) {
			var snap cniSnapshot
			snap.dbVMIDs = make(map[string]struct{})
			if err := c.store.Read(func(idx *networkIndex) error {
				for _, rec := range idx.Networks {
					if rec != nil {
						snap.dbVMIDs[rec.VMID] = struct{}{}
					}
				}
				return nil
			}); err != nil {
				return snap, err
			}
			// Scan named netns with the cocoon- prefix only.
			// Other tools (docker, containerd) may have their own entries.
			if entries, readErr := os.ReadDir(netnsBasePath); readErr == nil {
				for _, e := range entries {
					if name, ok := strings.CutPrefix(e.Name(), netnsPrefix); ok {
						snap.netnsNames = append(snap.netnsNames, name)
					}
				}
			}
			return snap, nil
		},
		Resolve: func(snap cniSnapshot, others map[string]any) []string {
			active := gc.Collect(others, gc.VMIDs)
			// Orphan = (DB VM IDs ∪ netns VM IDs) − active VM IDs.
			candidates := make(map[string]struct{}, len(snap.dbVMIDs)+len(snap.netnsNames))
			for id := range snap.dbVMIDs {
				candidates[id] = struct{}{}
			}
			for _, name := range snap.netnsNames {
				candidates[name] = struct{}{}
			}
			var orphans []string
			for id := range candidates {
				if _, ok := active[id]; !ok {
					orphans = append(orphans, id)
				}
			}
			slices.Sort(orphans)
			return orphans
		},
		Collect: func(ctx context.Context, ids []string) error {
			logger := log.WithFunc("cni.gc.Collect")
			var errs []error
			for _, vmID := range ids {
				// 1. Read CNI records for this VM (lockless — orchestrator holds flock).
				var records []networkRecord
				if readErr := c.store.Read(func(idx *networkIndex) error {
					records = idx.byVMID(vmID)
					return nil
				}); readErr != nil {
					errs = append(errs, fmt.Errorf("read records for %s: %w", vmID, readErr))
					continue
				}

				// 2. CNI DEL per NIC — best-effort IPAM release.
				if c.cniConf != nil && c.networkConfList != nil {
					nsPath := netnsPath(vmID)
					for _, rec := range records {
						rt := &libcni.RuntimeConf{
							ContainerID: vmID,
							NetNS:       nsPath,
							IfName:      rec.IfName,
						}
						if err := c.cniConf.DelNetworkList(ctx, c.networkConfList, rt); err != nil {
							logger.Warnf(ctx, "CNI DEL %s/%s: %v", vmID, rec.IfName, err)
						}
					}
				}

				// 3. Remove the named netns (with retry for async kernel fd cleanup).
				nsName := netnsName(vmID)
				if err := deleteNetns(ctx, nsName); err != nil && !os.IsNotExist(err) {
					errs = append(errs, fmt.Errorf("remove netns %s: %w", nsName, err))
				}

				// 4. Clean DB records (lockless write).
				if len(records) > 0 {
					if err := c.store.Write(func(idx *networkIndex) error {
						for id, rec := range idx.Networks {
							if rec != nil && rec.VMID == vmID {
								delete(idx.Networks, id)
							}
						}
						return nil
					}); err != nil {
						errs = append(errs, fmt.Errorf("clean DB for %s: %w", vmID, err))
					}
				}
			}
			return errors.Join(errs...)
		},
	}
}

// RegisterGC registers the CNI GC module with the given Orchestrator.
func (c *CNI) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, c.GCModule())
}
