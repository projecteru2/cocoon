package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/lock"
	"github.com/projecteru2/cocoon/lock/flock"
	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
)

// imageIndex is the top-level structure of the images.json file.
type imageIndex struct {
	cfg    *config.Config
	Images map[string]*imageEntry `json:"images"`
}

// newImageIndex creates an imageIndex bound to the given config.
func newImageIndex(cfg *config.Config) *imageIndex {
	return &imageIndex{cfg: cfg}
}

// With loads the image index under flock and passes it to fn.
// The lock is held for the duration of fn, ensuring consistent reads.
func (s *imageIndex) With(ctx context.Context, fn func(*imageIndex) error) error {
	return lock.WithLock(ctx, flock.New(s.cfg.ImageIndexLock()), func() error {
		s.Images = make(map[string]*imageEntry)
		data, err := os.ReadFile(s.cfg.ImageIndexFile()) //nolint:gosec // internal metadata
		if err != nil {
			if os.IsNotExist(err) {
				return fn(s)
			}
			return fmt.Errorf("read %s: %w", s.cfg.ImageIndexFile(), err)
		}
		if err := json.Unmarshal(data, s); err != nil {
			return fmt.Errorf("parse %s: %w", s.cfg.ImageIndexFile(), err)
		}
		if s.Images == nil {
			s.Images = make(map[string]*imageEntry)
		}
		return fn(s)
	})
}

// Update performs a read-modify-write on the image index under flock.
// The fn callback receives the loaded index and may mutate it. If fn returns nil
// the index is atomically written back (temp file -> fsync -> rename).
func (s *imageIndex) Update(ctx context.Context, fn func(*imageIndex) error) error {
	return s.With(ctx, func(idx *imageIndex) error {
		if err := fn(idx); err != nil {
			return err
		}
		return utils.AtomicWriteJSON(s.cfg.ImageIndexFile(), idx)
	})
}

// Lookup finds an image entry by ref (exact or normalized) or manifest digest.
// Returns the ref key, entry, and whether it was found.
func (s *imageIndex) Lookup(id string) (string, *imageEntry, bool) {
	if entry, ok := s.Images[id]; ok {
		return id, entry, true
	}
	// Try normalizing as an image reference (e.g., "ubuntu:24.04" -> "docker.io/library/ubuntu:24.04").
	if parsed, err := name.ParseReference(id); err == nil {
		normalized := parsed.String()
		if entry, ok := s.Images[normalized]; ok {
			return normalized, entry, true
		}
	}
	for ref, entry := range s.Images {
		if entry.ManifestDigest.String() == id {
			return ref, entry, true
		}
	}
	return "", nil, false
}

// referencedDigests returns all layer digest hex strings referenced by any image.
// KernelLayer/InitrdLayer are always a subset of Layers, so iterating Layers is sufficient.
func (s *imageIndex) referencedDigests() map[string]struct{} {
	refs := make(map[string]struct{})
	for _, entry := range s.Images {
		for _, layer := range entry.Layers {
			refs[layer.Digest.Hex()] = struct{}{}
		}
	}
	return refs
}

// imageEntry records one pulled OCI image.
// Paths are not stored; they are derived from digests and config at runtime.
type imageEntry struct {
	Ref            string        `json:"ref"`
	ManifestDigest types.Digest  `json:"manifest_digest"`
	Layers         []layerEntry  `json:"layers"`
	KernelLayer    types.Digest  `json:"kernel_layer"` // digest of layer containing vmlinuz
	InitrdLayer    types.Digest  `json:"initrd_layer"` // digest of layer containing initrd.img
	CreatedAt      time.Time     `json:"created_at"`
}

// layerEntry records one EROFS layer within an image.
type layerEntry struct {
	Digest types.Digest `json:"digest"`
}
