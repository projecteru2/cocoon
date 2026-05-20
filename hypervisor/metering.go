package hypervisor

import (
	"context"
	"time"

	"github.com/cocoonstack/cocoon/metering"
	"github.com/cocoonstack/cocoon/types"
)

func (b *Backend) makeEntry(kind metering.Kind, vmID string, reason metering.Reason, shape metering.Shape, now time.Time) metering.Entry {
	return metering.Entry{
		Kind: kind, VMID: vmID, Reason: reason,
		Hypervisor: b.Typ, Shape: shape, EmittedAt: now,
	}
}

func (b *Backend) emitAll(ctx context.Context, entries []metering.Entry) {
	for _, e := range entries {
		b.Metering.Emit(ctx, e)
	}
}

// emitOpenInterval fires the storage.start + compute.start pair; caller-provided now keeps adjacent stop/start timestamps aligned.
func (b *Backend) emitOpenInterval(ctx context.Context, vm *types.VM, reason metering.Reason, sourceSnapshotID string, now time.Time) {
	rec := b.Metering
	shape := shapeFromConfig(vm.Config)
	for _, kind := range []metering.Kind{metering.KindVMStorageStart, metering.KindVMComputeStart} {
		rec.Emit(ctx, metering.Entry{
			Kind: kind, VMID: vm.ID, SourceSnapshotID: sourceSnapshotID,
			Reason: reason, Hypervisor: b.Typ, Shape: shape, EmittedAt: now,
		})
	}
}

// emitDeleteClose fires storage.stop unconditionally; compute.stop only when an interval was open.
func (b *Backend) emitDeleteClose(ctx context.Context, vmID string, shape metering.Shape, computeReason metering.Reason, hadRunningInterval bool) {
	now := time.Now()
	rec := b.Metering
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
