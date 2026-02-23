package oci

import (
	"time"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/projecteru2/cocoon/config"
	storejson "github.com/projecteru2/cocoon/storage/json"
	"github.com/projecteru2/cocoon/types"
)

// imageIndex is the top-level structure of the OCI images.json file.
type imageIndex struct {
	Images map[string]*imageEntry `json:"images"`
}

// Init implements storage.Initer. Called automatically by Store after loading.
func (idx *imageIndex) Init() {
	if idx.Images == nil {
		idx.Images = make(map[string]*imageEntry)
	}
}

// Lookup finds an image entry by ref (exact or normalized) or manifest digest.
// Returns the ref key, entry, and whether it was found.
func (idx *imageIndex) Lookup(id string) (string, *imageEntry, bool) {
	if entry, ok := idx.Images[id]; ok {
		return id, entry, true
	}
	// Try normalizing as an image reference (e.g., "ubuntu:24.04" -> "docker.io/library/ubuntu:24.04").
	if parsed, err := name.ParseReference(id); err == nil {
		normalized := parsed.String()
		if entry, ok := idx.Images[normalized]; ok {
			return normalized, entry, true
		}
	}
	for ref, entry := range idx.Images {
		if entry.ManifestDigest.String() == id {
			return ref, entry, true
		}
	}
	return "", nil, false
}

// referencedDigests returns all layer digest hex strings referenced by any image.
// KernelLayer/InitrdLayer are always a subset of Layers, so iterating Layers is sufficient.
func (idx *imageIndex) referencedDigests() map[string]struct{} {
	refs := make(map[string]struct{})
	for _, entry := range idx.Images {
		for _, layer := range entry.Layers {
			refs[layer.Digest.Hex()] = struct{}{}
		}
	}
	return refs
}

// imageEntry records one pulled OCI image.
// Paths are not stored; they are derived from digests and config at runtime.
type imageEntry struct {
	Ref            string       `json:"ref"`
	ManifestDigest types.Digest `json:"manifest_digest"`
	Layers         []layerEntry `json:"layers"`
	KernelLayer    types.Digest `json:"kernel_layer"` // digest of layer containing vmlinuz
	InitrdLayer    types.Digest `json:"initrd_layer"` // digest of layer containing initrd.img
	CreatedAt      time.Time    `json:"created_at"`
}

// layerEntry records one EROFS layer within an image.
type layerEntry struct {
	Digest types.Digest `json:"digest"`
}

// newImageStore creates a JSON store for the OCI image index.
func newImageStore(cfg *config.Config) *storejson.Store[imageIndex] {
	return storejson.New[imageIndex](cfg.ImageIndexLock(), cfg.ImageIndexFile())
}
