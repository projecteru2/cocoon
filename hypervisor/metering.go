package hypervisor

import (
	"context"
	"time"

	"github.com/cocoonstack/cocoon/metering"
	"github.com/cocoonstack/cocoon/types"
)

func (b *Backend) meter() metering.Recorder {
	return metering.OrNop(b.Metering)
}

func (b *Backend) makeEntry(kind metering.Kind, vmID string, reason metering.Reason, shape metering.Shape, now time.Time) metering.Entry {
	return metering.Entry{
		Kind: kind, VMID: vmID, Reason: reason,
		Hypervisor: b.Typ, Shape: shape, EmittedAt: now,
	}
}

func (b *Backend) emitAll(ctx context.Context, entries []metering.Entry) {
	rec := b.meter()
	for _, e := range entries {
		rec.Emit(ctx, e)
	}
}

// emitOpenInterval emits the storage.start + compute.start pair that opens a fresh interval for cloned or restored VMs; the caller's now keeps the timestamp consistent with adjacent close events.
func (b *Backend) emitOpenInterval(ctx context.Context, vm *types.VM, reason metering.Reason, sourceSnapshotID string, now time.Time) {
	rec := b.meter()
	shape := shapeFromConfig(vm.Config)
	for _, kind := range []metering.Kind{metering.KindVMStorageStart, metering.KindVMComputeStart} {
		rec.Emit(ctx, metering.Entry{
			Kind: kind, VMID: vm.ID, SourceSnapshotID: sourceSnapshotID,
			Reason: reason, Hypervisor: b.Typ, Shape: shape, EmittedAt: now,
		})
	}
}

// emitDeleteClose emits storage.stop unconditionally and compute.stop only when the record had an open Running interval.
func (b *Backend) emitDeleteClose(ctx context.Context, vmID string, shape metering.Shape, computeReason metering.Reason, hadRunningInterval bool) {
	now := time.Now()
	rec := b.meter()
	if hadRunningInterval {
		rec.Emit(ctx, metering.Entry{
			Kind: metering.KindVMComputeStop, VMID: vmID, Reason: computeReason,
			Hypervisor: b.Typ, Shape: shape, EmittedAt: now,
		})
	}
	rec.Emit(ctx, metering.Entry{
		Kind: metering.KindVMStorageStop, VMID: vmID, Reason: metering.ReasonVMRemove,
		Hypervisor: b.Typ, Shape: shape, EmittedAt: now,
	})
}

func shapeFromConfig(c types.VMConfig) metering.Shape {
	return metering.Shape{
		CPU:          c.CPU,
		MemBytes:     c.Memory,
		StorageBytes: c.Storage,
	}
}
