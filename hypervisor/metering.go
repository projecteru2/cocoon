package hypervisor

import (
	"github.com/cocoonstack/cocoon/metering"
	"github.com/cocoonstack/cocoon/types"
)

// meter returns Backend.Metering or NopRecorder so emit sites can call .Emit without nil-checking.
func (b *Backend) meter() metering.Recorder {
	return metering.OrNop(b.Metering)
}

// shapeFromConfig builds a metering.Shape from a VMConfig's billable fields.
func shapeFromConfig(c types.VMConfig) metering.Shape {
	return metering.Shape{
		CPU:          c.CPU,
		MemBytes:     c.Memory,
		StorageBytes: c.Storage,
	}
}
