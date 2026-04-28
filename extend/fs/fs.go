// Package fs is the runtime attach interface for vhost-user-fs devices
// (typically backed by virtiofsd) on a running VM.
//
// State semantics: attach is runtime-only. Attached devices are not persisted
// in the VM record and disappear when the VM stops. The user must re-attach
// after a restart. Cocoon does not own the virtiofsd backend.
package fs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

const (
	DefaultNumQueues = 1
	DefaultQueueSize = 1024
)

var (
	// Tag charset is intentionally portable: usable as a CH device id suffix
	// (cocoon-fs-<tag>) and safe for shell quoting and guest mount commands.
	validTagRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,35}$`)

	// ErrUnsupportedBackend signals the resolved hypervisor backend cannot
	// hot-plug vhost-user-fs (e.g. Firecracker).
	ErrUnsupportedBackend = errors.New("backend does not support fs attach")
)

// Spec is one attach request.
type Spec struct {
	Socket    string
	Tag       string
	NumQueues int
	QueueSize int
}

// Attached is the inspect-time view of one fs device read from the
// running VM's CH config.
type Attached struct {
	ID     string `json:"id"`
	Tag    string `json:"tag"`
	Socket string `json:"socket"`
}

// Attacher hot-plugs and removes vhost-user-fs devices.
type Attacher interface {
	FsAttach(ctx context.Context, vmRef string, spec Spec) (deviceID string, err error)
	FsDetach(ctx context.Context, vmRef, tag string) error
}

// Lister enumerates currently-attached fs devices from running VM state.
type Lister interface {
	FsList(ctx context.Context, vmRef string) ([]Attached, error)
}

// Normalize enforces required fields and applies queue-size defaults.
// Mutates the receiver — callers expect the post-call Spec to be ready
// for serialization. Mirror vfio.Spec.NormalizedPath in spirit (compute
// + validate in one call) so future Specs in extend/ converge on the
// same idiom rather than fan out into pure-Validate vs. mutating-Validate
// styles.
func (s *Spec) Normalize() error {
	if s.Socket == "" {
		return fmt.Errorf("socket is required")
	}
	if !filepath.IsAbs(s.Socket) {
		return fmt.Errorf("socket must be absolute, got %q", s.Socket)
	}
	if s.Tag == "" {
		return fmt.Errorf("tag is required")
	}
	if !validTagRe.MatchString(s.Tag) {
		return fmt.Errorf("tag %q invalid: must match [A-Za-z0-9][A-Za-z0-9_-]{0,35}", s.Tag)
	}
	if s.NumQueues < 0 {
		return fmt.Errorf("num-queues must be non-negative, got %d", s.NumQueues)
	}
	if s.QueueSize < 0 {
		return fmt.Errorf("queue-size must be non-negative, got %d", s.QueueSize)
	}
	s.NumQueues = cmp.Or(s.NumQueues, DefaultNumQueues)
	s.QueueSize = cmp.Or(s.QueueSize, DefaultQueueSize)
	return nil
}

// DeriveID returns the deterministic CH device id for a tag. Both attach
// (passed to vm.add-fs) and detach (matched against vm.info) use this so
// concurrent attaches of the same tag fail at CH's id-uniqueness check.
func DeriveID(tag string) string {
	return "cocoon-fs-" + tag
}
