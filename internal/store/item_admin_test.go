package store

import (
	"context"
	"path/filepath"
	"slices"
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

func TestSetItemOrder(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	gid, ids := makeGalleryWithItems(t, st, 3)
	want := []int64{ids[2], ids[0], ids[1]}

	if err := st.SetItemOrder(ctx, gid, want); err != nil {
		t.Fatal(err)
	}
	if got := orderIDs(t, st, gid); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	if err := st.SetItemOrder(ctx, gid, []int64{ids[0], ids[0], ids[1]}); err == nil {
		t.Fatal("duplicate item order succeeded")
	}
	if got := orderIDs(t, st, gid); !slices.Equal(got, want) {
		t.Fatalf("order after rejected update = %v, want %v", got, want)
	}
}

func TestSetGalleryItemOrderAlphabetically(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	gid, ids := makeGalleryWithItems(t, st, 3)

	if err := st.MoveItem(ctx, gid, ids[2], true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGalleryItemOrder(ctx, gid, model.SortByFilename, model.SortAscending); err != nil {
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

func TestSetGalleryItemOrderDescending(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	gid, ids := makeGalleryWithItems(t, st, 3)

	if err := st.SetGalleryItemOrder(ctx, gid, model.SortByFilename, model.SortDescending); err != nil {
		t.Fatal(err)
	}
	if got, want := orderIDs(t, st, gid), []int64{ids[2], ids[1], ids[0]}; !slices.Equal(got, want) {
		t.Fatalf("descending filename order = %v, want %v", got, want)
	}

	if _, err := st.DB.ExecContext(ctx, `UPDATE items SET taken_at = CASE id WHEN ? THEN '2024-01-01' WHEN ? THEN NULL WHEN ? THEN '2025-01-01' END WHERE gallery_id = ?`, ids[0], ids[1], ids[2], gid); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGalleryItemOrder(ctx, gid, model.SortByDate, model.SortDescending); err != nil {
		t.Fatal(err)
	}
	if got, want := orderIDs(t, st, gid), []int64{ids[2], ids[0], ids[1]}; !slices.Equal(got, want) {
		t.Fatalf("descending date order = %v, want %v", got, want)
	}
}

func TestSetGalleryItemOrderByDateAddedDescending(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	gid, ids := makeGalleryWithItems(t, st, 3)

	if _, err := st.DB.ExecContext(ctx, `UPDATE items SET created_at = CASE id WHEN ? THEN '2025-01-01' WHEN ? THEN '2026-01-01' WHEN ? THEN '2024-01-01' END WHERE gallery_id = ?`, ids[0], ids[1], ids[2], gid); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGalleryItemOrder(ctx, gid, model.SortByDateAdded, model.SortDescending); err != nil {
		t.Fatal(err)
	}
	if got, want := orderIDs(t, st, gid), []int64{ids[1], ids[0], ids[2]}; !slices.Equal(got, want) {
		t.Fatalf("descending date-added order = %v, want %v", got, want)
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

func TestItemTextMetadataPreservesEdits(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, ids := makeGalleryWithItems(t, st, 1)

	if err := st.FillItemTextMetadata(ctx, ids[0], "Imported title", "Imported description"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateItemPresentation(ctx, ids[0], "Edited title", "Edited description", "Caption", model.ItemPublished, false, "", "", "Manual lens", "Manual lens"); err != nil {
		t.Fatal(err)
	}
	if err := st.FillItemTextMetadata(ctx, ids[0], "Replacement title", "Replacement description"); err != nil {
		t.Fatal(err)
	}

	it, err := st.Item(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "Edited title" || it.Description != "Edited description" || it.Caption != "Caption" || it.ManualLens != "Manual lens" || it.Lens != "Manual lens" {
		t.Fatalf("presentation metadata was overwritten: %+v", it)
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
	it.EmbeddedLens = "RF 50mm F1.2"
	it.XMPLens = "Adobe fallback"
	it.LightroomLens = "Catalog lens"
	if err := st.SetItemLightroomLens(ctx, it.ID, it.LightroomLens, it.LightroomLens); err != nil {
		t.Fatal(err)
	}
	it.ISO = "400"
	it.Focal = "50 mm"
	if err := st.UpdateItemEXIF(ctx, it); err != nil {
		t.Fatal(err)
	}

	got, err := st.Item(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.Camera != "Canon EOS R5" || got.EmbeddedCamera != "Canon EOS R5" || got.EmbeddedLens != "RF 50mm F1.2" ||
		got.LightroomLens != "Catalog lens" || got.XMPLens != "Adobe fallback" || got.ISO != "400" || got.Focal != "50 mm" {
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

func TestCameraLensClues(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	gid, _ := makeGalleryWithItems(t, st, 4)
	items, err := st.ItemsByGallery(ctx, gid)
	if err != nil {
		t.Fatal(err)
	}
	items[0].Camera = "FUJIFILM XF10"
	items[0].Focal = "18.5 mm"
	items[0].EXIF = `{"MaxApertureValue":["297/100"]}`
	items[1].Camera = "FUJIFILM XF10"
	items[1].Focal = "18.5 mm"
	items[1].EXIF = `{"MaxApertureValue":["297/100"]}`
	items[2].Camera = "Canon EOS R5"
	items[2].Lens = "RF 50mm F1.2"
	items[2].EmbeddedLens = "RF 50mm F1.2"
	items[3].Camera = "  "
	for _, item := range items {
		if err := st.UpdateItemEXIF(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	clues, err := st.CameraLensClues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(clues) != 1 || clues[0].Camera != "FUJIFILM XF10" || clues[0].Focal != "18.5 mm" ||
		clues[0].MaxApertureAPEX != "297/100" || clues[0].Count != 2 {
		t.Fatalf("clues = %+v", clues)
	}
}

func TestXMPProfileUsages(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	gid, _ := makeGalleryWithItems(t, st, 3)
	items, err := st.ItemsByGallery(ctx, gid)
	if err != nil {
		t.Fatal(err)
	}
	items[0].Camera, items[0].XMPLens = "FUJIFILM GFX 50R", "Voigtlander 15mm"
	items[0].SidecarLens = "Voigtlander 15mm f/4.5"
	items[1].Camera, items[1].XMPLens = "FUJIFILM GFX 50R", "Voigtlander 15mm"
	items[2].Camera, items[2].XMPLens = "Leica M10", "Voigtlander 15mm"
	for _, item := range items {
		if err := st.UpdateItemEXIF(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	usages, err := st.XMPProfileUsages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 2 || usages[0].Camera != "FUJIFILM GFX 50R" || usages[0].Count != 2 || usages[0].SidecarCount != 1 ||
		usages[1].Camera != "Leica M10" || usages[1].Count != 1 {
		t.Fatalf("profile usages = %+v", usages)
	}
}

func TestLensSuggestionsPreferManualUsage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, ids := makeGalleryWithItems(t, st, 3)
	if err := st.UpdateItemPresentation(ctx, ids[0], "", "", "", model.ItemPublished, false, "", "", "", "Automatic lens"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateItemPresentation(ctx, ids[1], "", "", "", model.ItemPublished, false, "", "", "", "Automatic lens"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateItemPresentation(ctx, ids[2], "", "", "", model.ItemPublished, false, "", "", "Manual lens", "Manual lens"); err != nil {
		t.Fatal(err)
	}

	suggestions, err := st.LensSuggestions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != 2 || suggestions[0].Name != "Manual lens" || suggestions[0].ManualCount != 1 ||
		suggestions[1].Name != "Automatic lens" || suggestions[1].Count != 2 {
		t.Fatalf("suggestions = %+v", suggestions)
	}
}

func TestCameraSuggestionsPreferManualUsage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, ids := makeGalleryWithItems(t, st, 3)
	if err := st.UpdateItemPresentation(ctx, ids[0], "", "", "", model.ItemPublished, false, "", "Automatic camera", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateItemPresentation(ctx, ids[1], "", "", "", model.ItemPublished, false, "", "Automatic camera", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateItemPresentation(ctx, ids[2], "", "", "", model.ItemPublished, false, "Leica M6", "Leica M6", "", ""); err != nil {
		t.Fatal(err)
	}

	suggestions, err := st.CameraSuggestions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != 2 || suggestions[0].Name != "Leica M6" || suggestions[0].ManualCount != 1 ||
		suggestions[1].Name != "Automatic camera" || suggestions[1].Count != 2 {
		t.Fatalf("suggestions = %+v", suggestions)
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
