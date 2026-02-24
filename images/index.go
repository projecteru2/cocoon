package images

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/projecteru2/cocoon/types"
	"github.com/projecteru2/cocoon/utils"
	"github.com/projecteru2/core/log"
)

const minHexLen = 12

// Entry defines the common behavior of an image index entry.
// Both OCI and cloudimg imageEntry types implement this with value receivers.
type Entry interface {
	EntryID() string
	EntryRef() string
	EntryCreatedAt() time.Time
	DigestHexes() []string
}

// Index is the shared generic base for image indices.
// Both backends embed Index[imageEntry] to inherit Init() and the Images map.
type Index[E any] struct {
	Images map[string]*E `json:"images"`
}

// Init implements storage.Initer. Called automatically by storejson.Store after loading.
func (idx *Index[E]) Init() {
	if idx.Images == nil {
		idx.Images = make(map[string]*E)
	}
}

// ReferencedDigests collects all blob digest hex strings referenced by any entry.
func ReferencedDigests[E Entry](images map[string]*E) map[string]struct{} {
	refs := make(map[string]struct{})
	for _, ep := range images {
		if ep == nil {
			continue
		}
		e := *ep
		for _, hex := range e.DigestHexes() {
			refs[hex] = struct{}{}
		}
	}
	return refs
}

// LookupRefs returns all ref keys matching id by exact key, optional
// normalization, or digest prefix. normalizers are tried in order for
// backend-specific key transforms (e.g., OCI image reference normalization).
func LookupRefs[E Entry](images map[string]*E, id string, normalizers ...func(string) (string, bool)) []string {
	// Exact key match.
	if entry, ok := images[id]; ok && entry != nil {
		return []string{id}
	}
	// Try normalizers (e.g., OCI "ubuntu:24.04" -> "docker.io/library/ubuntu:24.04").
	for _, norm := range normalizers {
		if normalized, ok := norm(id); ok {
			if entry, ok := images[normalized]; ok && entry != nil {
				return []string{normalized}
			}
		}
	}
	// Digest match (exact or prefix) — collect ALL matching refs.
	// Require at least minHexLen hex characters for prefix match to avoid
	// overly broad matches (e.g., "sha256:a" hitting everything).
	// Strip optional "sha256:" before measuring so the threshold counts
	// actual hex digits, not the algorithm prefix.
	idHex := strings.TrimPrefix(id, "sha256:")
	var refs []string
	for ref, ep := range images {
		if ep == nil {
			continue
		}
		e := *ep
		dStr := e.EntryID()
		dHex := strings.TrimPrefix(dStr, "sha256:")
		if dStr == id || dHex == id {
			refs = append(refs, ref)
			continue
		}
		if len(idHex) >= minHexLen && (strings.HasPrefix(dHex, idHex)) {
			refs = append(refs, ref)
		}
	}
	return refs
}

// DeleteByID removes entries from the map by looking up each ID.
// lookup returns all matching ref keys (supporting digest prefix and multi-ref
// matches), so "delete <digest>" removes every ref pointing to that digest.
func DeleteByID[E any](ctx context.Context, logPrefix string, images map[string]*E, lookup func(string) []string, ids []string) []string {
	logger := log.WithFunc(logPrefix)
	var deleted []string
	for _, id := range ids {
		refs := lookup(id)
		if len(refs) == 0 {
			logger.Infof(ctx, "image %q not found, skipping", id)
			continue
		}
		for _, ref := range refs {
			delete(images, ref)
			deleted = append(deleted, ref)
			logger.Infof(ctx, "deleted from index: %s", ref)
		}
	}
	return deleted
}

// EntryToImage converts a single index entry to *types.Image.
// Cheaper than ListImages for single-entry lookups — no map or slice allocation.
func EntryToImage[E Entry](entry *E, typ string, sizer func(*E) int64) *types.Image {
	if entry == nil {
		return nil
	}
	e := *entry
	return &types.Image{
		ID:        e.EntryID(),
		Name:      e.EntryRef(),
		Type:      typ,
		Size:      sizer(entry),
		CreatedAt: e.EntryCreatedAt(),
	}
}

// ListImages iterates the index and builds a list of types.Image.
// sizer is provided by each backend to compute per-entry disk usage.
func ListImages[E Entry](images map[string]*E, typ string, sizer func(*E) int64) []*types.Image {
	var result []*types.Image
	for _, ep := range images {
		if ep == nil {
			continue
		}
		e := *ep
		result = append(result, &types.Image{
			ID:        e.EntryID(),
			Name:      e.EntryRef(),
			Type:      typ,
			Size:      sizer(ep),
			CreatedAt: e.EntryCreatedAt(),
		})
	}
	return result
}

// ScanBlobHexes returns the digest hexes of all files with the given suffix in dir.
// Used by GC ReadDB implementations to build the on-disk candidate set.
func ScanBlobHexes(dir, suffix string) []string {
	entries, _ := os.ReadDir(dir)
	var hexes []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			hexes = append(hexes, strings.TrimSuffix(e.Name(), suffix))
		}
	}
	return hexes
}

// FilterUnreferenced returns elements of candidates not present in refs.
// Used by GC Resolve implementations to compute the deletion set.
func FilterUnreferenced(candidates []string, refs map[string]struct{}) []string {
	var out []string
	for _, s := range candidates {
		if _, ok := refs[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}

// GCStaleTemp removes temp entries older than StaleTempAge.
// Set dirOnly=true to only remove directories (OCI uses dirs, cloudimg uses files).
func GCStaleTemp(ctx context.Context, dir string, dirOnly bool) []error {
	cutoff := time.Now().Add(-utils.StaleTempAge)
	return utils.RemoveMatching(ctx, dir, func(e os.DirEntry) bool {
		if dirOnly && !e.IsDir() {
			return false
		}
		info, err := e.Info()
		return err == nil && info.ModTime().Before(cutoff)
	})
}

// GCUnreferencedBlobs removes files with the given suffix that aren't in refs.
func GCUnreferencedBlobs(ctx context.Context, dir, suffix string, refs map[string]struct{}) []error {
	return utils.RemoveMatching(ctx, dir, func(e os.DirEntry) bool {
		n := e.Name()
		if !strings.HasSuffix(n, suffix) {
			return false
		}
		_, ok := refs[strings.TrimSuffix(n, suffix)]
		return !ok
	})
}

// GCUnreferencedDirs removes directories not in refs (used by OCI for boot dirs).
func GCUnreferencedDirs(ctx context.Context, dir string, refs map[string]struct{}) []error {
	return utils.RemoveMatching(ctx, dir, func(e os.DirEntry) bool {
		if !e.IsDir() {
			return false
		}
		_, ok := refs[e.Name()]
		return !ok
	})
}
