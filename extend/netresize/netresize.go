// Package netresize is the runtime interface for resizing a VM's NIC count.
// Cocoon never touches the guest — the user must quiesce in-guest NIC state
// (driver unbind / NetworkManager / NDIS) before reducing the count.
package netresize

import (
	"context"
	"errors"
	"fmt"

	"github.com/cocoonstack/cocoon/network"
	"github.com/cocoonstack/cocoon/types"
)

var ErrUnsupportedBackend = errors.New("backend does not support net resize")

// Spec is one resize request.
type Spec struct {
	Target int
}

// NIC is one NIC's summary, returned in Result.Added / Result.Removed.
type NIC struct {
	Index int    `json:"index"`
	TAP   string `json:"tap"`
	MAC   string `json:"mac"`
}

// Result reports the before/after count and the NICs touched by the call.
type Result struct {
	Before  int   `json:"before"`
	After   int   `json:"after"`
	Added   []NIC `json:"added,omitempty"`
	Removed []NIC `json:"removed,omitempty"`
}

// Plumbing is the host-side network operations NetResize delegates to.
// network.Network satisfies this implicitly.
type Plumbing interface {
	Add(ctx context.Context, vmID string, vmCfg *types.VMConfig, specs ...network.AddSpec) ([]*types.NetworkConfig, error)
	Remove(ctx context.Context, vmID string, indices ...int) error
}

// Resizer resizes the NIC count on a running VM.
type Resizer interface {
	NetResize(ctx context.Context, vmRef string, spec Spec, plumbing Plumbing) (Result, error)
}

// Normalize validates the spec.
func (s *Spec) Normalize() error {
	if s.Target < 0 {
		return fmt.Errorf("--nics must be non-negative, got %d", s.Target)
	}
	return nil
}
