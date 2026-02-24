package cloudimg

import (
	"time"

	"github.com/projecteru2/cocoon/config"
	"github.com/projecteru2/cocoon/images"
	"github.com/projecteru2/cocoon/lock/flock"
	storejson "github.com/projecteru2/cocoon/storage/json"
	"github.com/projecteru2/cocoon/types"
)

// imageIndex is the top-level structure of the cloudimg images.json file.
type imageIndex struct {
	images.Index[imageEntry]
}

// Lookup finds an image entry by URL or content digest.
// Returns the ref key, entry, and whether it was found.
func (idx *imageIndex) Lookup(id string) (string, *imageEntry, bool) {
	// Exact URL match.
	if entry, ok := idx.Images[id]; ok && entry != nil {
		return id, entry, true
	}
	// Search by content digest.
	for ref, entry := range idx.Images {
		if entry != nil && (entry.ContentSum.String() == id || entry.ContentSum.Hex() == id) {
			return ref, entry, true
		}
	}
	return "", nil, false
}

// LookupRefs returns all ref keys matching id for DeleteByID.
// Delegates to shared images.LookupRefs (no normalizers needed for URLs).
func (idx *imageIndex) LookupRefs(id string) []string {
	return images.LookupRefs(idx.Images, id)
}

// imageEntry records one pulled cloud image.
type imageEntry struct {
	Ref        string       `json:"ref"`         // Original URL.
	ContentSum types.Digest `json:"content_sum"` // SHA-256 of downloaded content.
	Size       int64        `json:"size"`        // qcow2 blob size on disk.
	CreatedAt  time.Time    `json:"created_at"`
}

// images.Entry implementation (value receivers).

func (e imageEntry) EntryID() string           { return e.ContentSum.String() }
func (e imageEntry) EntryRef() string          { return e.Ref }
func (e imageEntry) EntryCreatedAt() time.Time { return e.CreatedAt }
func (e imageEntry) DigestHexes() []string     { return []string{e.ContentSum.Hex()} }

// newImageStore creates a JSON store for the cloudimg image index.
func newImageStore(cfg *config.Config) *storejson.Store[imageIndex] {
	return storejson.New[imageIndex](cfg.CloudimgIndexFile(), flock.New(cfg.CloudimgIndexLock()))
}
