package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tkjaer/curator/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "cms.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func makeGalleryWithItems(t *testing.T, st *Store, n int) (int64, []int64) {
	t.Helper()
	ctx := context.Background()
	gid, err := st.CreateGallery(ctx, model.Gallery{Slug: "g", Title: "G", Type: model.GalleryGrid})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := 0; i < n; i++ {
		id, err := st.CreateItem(ctx, model.Item{
			GalleryID: gid, OriginalPath: "g/x", Filename: string(rune('a'+i)) + ".jpg",
			Status: model.ItemPublished, SortOrder: i + 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return gid, ids
}

func orderIDs(t *testing.T, st *Store, gid int64) []int64 {
	t.Helper()
	items, err := st.ItemsByGallery(context.Background(), gid)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func TestMoveItem(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	gid, ids := makeGalleryWithItems(t, st, 3) // order: ids[0], ids[1], ids[2]

	if err := st.MoveItem(ctx, gid, ids[2], true); err != nil { // move last up
		t.Fatal(err)
	}
	got := orderIDs(t, st, gid)
	want := []int64{ids[0], ids[2], ids[1]}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestSetGalleryItemOrderAlphabetically(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	gid, ids := makeGalleryWithItems(t, st, 3)

	if err := st.MoveItem(ctx, gid, ids[2], true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGalleryItemOrder(ctx, gid, model.SortByFilename); err != nil {
		t.Fatal(err)
	}

	got := orderIDs(t, st, gid)
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("alphabetical order = %v, want %v", got, ids)
		}
	}
	g, err := st.Gallery(ctx, gid)
	if err != nil {
		t.Fatal(err)
	}
	if g.SortMode != model.SortByFilename {
		t.Fatalf("sort mode = %q, want filename", g.SortMode)
	}
}

func TestDeleteItemClearsCover(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	gid, ids := makeGalleryWithItems(t, st, 2)

	if err := st.SetGalleryCover(ctx, gid, &ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteItem(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}

	if n, _ := st.CountItems(ctx, gid); n != 1 {
		t.Errorf("count = %d, want 1 after delete", n)
	}
	g, err := st.Gallery(ctx, gid)
	if err != nil {
		t.Fatal(err)
	}
	if g.CoverItemID != nil {
		t.Errorf("cover = %v, want nil after deleting the cover item", *g.CoverItemID)
	}
}

func TestUpdateItemFields(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, ids := makeGalleryWithItems(t, st, 1)

	if err := st.UpdateItemFields(ctx, ids[0], "Sunset", model.ItemUnlisted, true); err != nil {
		t.Fatal(err)
	}
	it, err := st.Item(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if it.Caption != "Sunset" || it.Status != model.ItemUnlisted || !it.Highlighted {
		t.Errorf("update not applied: %+v", it)
	}
}

func TestUpdateItemEXIF(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, ids := makeGalleryWithItems(t, st, 1)

	it, err := st.Item(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	it.Camera = "Canon EOS R5"
	it.ISO = "400"
	it.Focal = "50 mm"
	if err := st.UpdateItemEXIF(ctx, it); err != nil {
		t.Fatal(err)
	}

	got, err := st.Item(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.Camera != "Canon EOS R5" || got.ISO != "400" || got.Focal != "50 mm" {
		t.Errorf("EXIF not updated: %+v", got)
	}

	all, err := st.AllItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("AllItems returned %d, want 1", len(all))
	}
}

func TestMoveGalleryCyclePrevention(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mk := func(slug string, parent *int64) int64 {
		id, err := st.CreateGallery(ctx, model.Gallery{Slug: slug, Title: slug, ParentID: parent})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := mk("a", nil)
	b := mk("b", &a)
	c := mk("c", &b) // a > b > c

	// Valid: move c to the top level.
	if err := st.MoveGallery(ctx, c, nil); err != nil {
		t.Fatalf("move c to top level: %v", err)
	}

	// Invalid: a into itself.
	if err := st.MoveGallery(ctx, a, &a); err == nil {
		t.Error("expected error moving a into itself")
	}
	// Invalid: a under its descendant b.
	if err := st.MoveGallery(ctx, c, &b); err != nil { // re-nest c under b first
		t.Fatal(err)
	}
	if err := st.MoveGallery(ctx, a, &b); err == nil {
		t.Error("expected error moving a under its descendant b")
	}

	// Valid: move b (with c) under the now-top-level nothing... move a under c's old spot is cyclic; instead
	// verify a valid reparent: move b to top level.
	if err := st.MoveGallery(ctx, b, nil); err != nil {
		t.Fatalf("move b to top level: %v", err)
	}
}
