package images

import (
	"context"

	"github.com/projecteru2/cocoon/storage"
	"github.com/projecteru2/cocoon/types"
)

// InspectEntry reads one entry by id and converts it to types.Image.
// Returns (nil, nil) when no entry matches.
func InspectEntry[I any, E Entry](
	ctx context.Context,
	store storage.Store[I],
	id, typ string,
	lookupRefs func(*I, string) []string,
	entries func(*I) map[string]*E,
	sizer func(*E) int64,
) (result *types.Image, err error) {
	err = store.With(ctx, func(idx *I) error {
		refs := lookupRefs(idx, id)
		if len(refs) == 0 {
			return nil
		}
		result = EntryToImage(entries(idx)[refs[0]], typ, sizer)
		return nil
	})
	return
}

// ListEntries reads all entries and converts them to []types.Image.
func ListEntries[I any, E Entry](
	ctx context.Context,
	store storage.Store[I],
	typ string,
	entries func(*I) map[string]*E,
	sizer func(*E) int64,
) (result []*types.Image, err error) {
	err = store.With(ctx, func(idx *I) error {
		result = ListImages(entries(idx), typ, sizer)
		return nil
	})
	return
}

// DeleteEntries deletes entries from an index by ids and returns removed refs.
func DeleteEntries[I any, E any](
	ctx context.Context,
	store storage.Store[I],
	logPrefix string,
	ids []string,
	entries func(*I) map[string]*E,
	lookupRefs func(*I, string) []string,
) (deleted []string, err error) {
	err = store.Update(ctx, func(idx *I) error {
		deleted = DeleteByID(ctx, logPrefix, entries(idx), func(id string) []string {
			return lookupRefs(idx, id)
		}, ids)
		return nil
	})
	return
}
