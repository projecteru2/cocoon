package cloudimg

import (
	"time"

	"github.com/projecteru2/cocoon/config"
	storejson "github.com/projecteru2/cocoon/storage/json"
	"github.com/projecteru2/cocoon/types"
)

// imageIndex is the top-level structure of the cloudimg images.json file.
type imageIndex struct {
	Images map[string]*imageEntry `json:"images"`
}

// Init implements storage.Initer. Called automatically by Store after loading.
func (idx *imageIndex) Init() {
	if idx.Images == nil {
		idx.Images = make(map[string]*imageEntry)
	}
}

// Lookup finds an image entry by URL or content digest.
// Returns the ref key, entry, and whether it was found.
func (idx *imageIndex) Lookup(id string) (string, *imageEntry, bool) {
	// Exact URL match.
	if entry, ok := idx.Images[id]; ok {
		return id, entry, true
	}
	// Search by content digest.
	for ref, entry := range idx.Images {
		if entry.ContentSum.String() == id || entry.ContentSum.Hex() == id {
			return ref, entry, true
		}
	}
	return "", nil, false
}

// referencedDigests returns all content digest hex strings referenced by any image.
func (idx *imageIndex) referencedDigests() map[string]struct{} {
	refs := make(map[string]struct{})
	for _, entry := range idx.Images {
		refs[entry.ContentSum.Hex()] = struct{}{}
	}
	return refs
}

// imageEntry records one pulled cloud image.
type imageEntry struct {
	Ref        string       `json:"ref"`         // Original URL.
	ContentSum types.Digest `json:"content_sum"` // SHA-256 of downloaded content.
	Size       int64        `json:"size"`         // qcow2 blob size on disk.
	CreatedAt  time.Time    `json:"created_at"`
}

// newImageStore creates a JSON store for the cloudimg image index.
func newImageStore(cfg *config.Config) *storejson.Store[imageIndex] {
	return storejson.New[imageIndex](cfg.CloudimgIndexLock(), cfg.CloudimgIndexFile())
}
