package localfile

import (
	"context"
	"time"

	"github.com/cocoonstack/cocoon/metering"
)

func emitSnapStart(ctx context.Context, rec metering.Recorder, snapID, hypType string, size int64, at time.Time) {
	rec.Emit(ctx, metering.Entry{
		Kind: metering.KindSnapStorageStart, SnapshotID: snapID, Hypervisor: hypType,
		Shape: metering.Shape{StorageBytes: size}, EmittedAt: at,
	})
}

func emitSnapStop(ctx context.Context, rec metering.Recorder, snapID, hypType string) {
	rec.Emit(ctx, metering.Entry{
		Kind: metering.KindSnapStorageStop, SnapshotID: snapID,
		Reason: metering.ReasonSnapRemove, Hypervisor: hypType, EmittedAt: time.Now(),
	})
}
