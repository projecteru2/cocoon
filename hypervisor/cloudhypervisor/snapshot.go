package cloudhypervisor

import (
	"context"
	"io"

	"github.com/projecteru2/cocoon/types"
)

// Snapshot exports the VM's disk data as a tar stream.
func (ch *CloudHypervisor) Snapshot(_ context.Context, _ string) (*types.SnapshotConfig, io.ReadCloser, error) {
	panic("not implemented")
}
