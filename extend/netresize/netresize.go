// Package netresize is the runtime interface for resizing a VM's NIC count.
// Cocoon never touches the guest — the user must quiesce in-guest NIC state
// (driver unbind / NetworkManager / NDIS) before reducing the count.
package netresize

import (
	"context"
	"errors"
	"fmt"
)

var ErrUnsupportedBackend = errors.New("backend does not support net resize")

// Spec is one resize request.
type Spec struct {
	Target           int
	KeepHostOnRemove bool
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

// Resizer resizes the NIC count on a running VM.
type Resizer interface {
	NetResize(ctx context.Context, vmRef string, spec Spec) (Result, error)
}

// Normalize validates the spec. Returns an error if Target is negative.
// Named for parity with extend/fs.Spec.Normalize even though no defaulting
// is needed here.
func (s *Spec) Normalize() error {
	if s.Target < 0 {
		return fmt.Errorf("--nics must be non-negative, got %d", s.Target)
	}
	return nil
}
