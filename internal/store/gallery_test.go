package store

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/tkjaer/curator/internal/model"
)

func galleryByID(t *testing.T, st *Store, id int64) model.Gallery {
	t.Helper()
	all, err := st.Galleries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range all {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("gallery %d not found", id)
	return model.Gallery{}
}

func TestReservedRootGallerySlugs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	parentID, err := st.CreateGallery(ctx, model.Gallery{Slug: "parent", Title: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	for _, gallerySlug := range []string{"_curator", "browse", "feed.xml"} {
		if _, err := st.CreateGallery(ctx, model.Gallery{Slug: gallerySlug, Title: gallerySlug}); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("CreateGallery(%q) error = %v, want reserved slug error", gallerySlug, err)
		}
		childID, err := st.CreateGallery(ctx, model.Gallery{ParentID: &parentID, Slug: gallerySlug, Title: gallerySlug})
		if err != nil {
			t.Fatalf("nested CreateGallery(%q): %v", gallerySlug, err)
		}
		if err := st.MoveGallery(ctx, childID, nil); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("MoveGallery(%q) error = %v, want reserved slug error", gallerySlug, err)
		}
		if _, _, err := st.UpsertExternalGallery(ctx, "test", gallerySlug, model.Gallery{Slug: gallerySlug, Title: gallerySlug}); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("UpsertExternalGallery(%q) error = %v, want reserved slug error", gallerySlug, err)
		}
	}
}

func TestUpdateGalleryTitleDoesNotChangeSlug(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id, err := st.CreateGallery(ctx, model.Gallery{Slug: "original-url", Title: "Original title"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateGalleryTitle(ctx, id, "New title"); err != nil {
		t.Fatal(err)
	}
	g, err := st.Gallery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if g.Title != "New title" || g.Slug != "original-url" {
		t.Fatalf("updated gallery = %#v", g)
	}
}

func TestUpdateGallerySlugValidatesConflicts(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	first, err := st.CreateGallery(ctx, model.Gallery{Slug: "first", Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateGallery(ctx, model.Gallery{Slug: "second", Title: "Second"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateGallerySlug(ctx, first, "second"); err == nil {
		t.Fatal("expected sibling slug conflict")
	}
	if err := st.UpdateGallerySlug(ctx, first, "browse"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved slug error = %v", err)
	}
	if err := st.UpdateGallerySlug(ctx, first, "renamed"); err != nil {
		t.Fatal(err)
	}
	g, err := st.Gallery(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if g.Slug != "renamed" {
		t.Fatalf("slug = %q, want renamed", g.Slug)
	}
}

func TestMoveGalleryOrderOnlyReordersSiblings(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	firstID, err := st.CreateGallery(ctx, model.Gallery{Slug: "first", Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := st.CreateGallery(ctx, model.Gallery{Slug: "second", Title: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	thirdID, err := st.CreateGallery(ctx, model.Gallery{Slug: "third", Title: "Third"})
	if err != nil {
		t.Fatal(err)
	}
	childA, err := st.CreateGallery(ctx, model.Gallery{ParentID: &firstID, Slug: "a", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	childB, err := st.CreateGallery(ctx, model.Gallery{ParentID: &firstID, Slug: "b", Title: "B"})
	if err != nil {
		t.Fatal(err)
	}

	if err := st.MoveGalleryOrder(ctx, thirdID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveGalleryOrder(ctx, childB, true); err != nil {
		t.Fatal(err)
	}
	galleries, err := st.Galleries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var roots, children []int64
	for _, gallery := range galleries {
		if gallery.ParentID == nil {
			roots = append(roots, gallery.ID)
		} else if *gallery.ParentID == firstID {
			children = append(children, gallery.ID)
		}
	}
	if !reflect.DeepEqual(roots, []int64{firstID, thirdID, secondID}) {
		t.Errorf("root order = %v", roots)
	}
	if !reflect.DeepEqual(children, []int64{childB, childA}) {
		t.Errorf("child order = %v", children)
	}
}

func TestCreateGalleryAppendsAfterCustomSiblingOrder(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	var ids []int64
	for _, gallerySlug := range []string{"first", "second", "third"} {
		id, err := st.CreateGallery(ctx, model.Gallery{Slug: gallerySlug, Title: gallerySlug})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := st.MoveGalleryOrder(ctx, ids[2], true); err != nil {
		t.Fatal(err)
	}
	lastID, err := st.CreateGallery(ctx, model.Gallery{Slug: "last", Title: "last"})
	if err != nil {
		t.Fatal(err)
	}

	galleries, err := st.Galleries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, gallery := range galleries {
		if gallery.ParentID == nil {
			got = append(got, gallery.ID)
		}
	}
	want := []int64{ids[0], ids[2], ids[1], lastID}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("root order after insert = %v, want %v", got, want)
	}
}

func TestGalleryOrderMigrationAppendsMisplacedGalleries(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	var rootIDs []int64
	for _, gallerySlug := range []string{"first", "second", "third", "last"} {
		id, err := st.CreateGallery(ctx, model.Gallery{Slug: gallerySlug, Title: gallerySlug})
		if err != nil {
			t.Fatal(err)
		}
		rootIDs = append(rootIDs, id)
	}
	if err := st.MoveGalleryOrder(ctx, rootIDs[2], true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE galleries SET sort_order = 0 WHERE id = ?`, rootIDs[3]); err != nil {
		t.Fatal(err)
	}
	parentID, err := st.CreateGallery(ctx, model.Gallery{Slug: "parent", Title: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	for _, gallerySlug := range []string{"automatic-b", "automatic-a"} {
		if _, err := st.CreateGallery(ctx, model.Gallery{ParentID: &parentID, Slug: gallerySlug, Title: gallerySlug}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = 26`); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	galleries, err := st.Galleries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var roots []int64
	for _, gallery := range galleries {
		if gallery.ParentID == nil && gallery.ID != parentID {
			roots = append(roots, gallery.ID)
		}
		if gallery.ParentID != nil && *gallery.ParentID == parentID && gallery.SortOrder != 0 {
			t.Errorf("automatic child %q sort order = %d, want 0", gallery.Slug, gallery.SortOrder)
		}
	}
	want := []int64{rootIDs[0], rootIDs[2], rootIDs[1], rootIDs[3]}
	if !reflect.DeepEqual(roots, want) {
		t.Errorf("migrated root order = %v, want %v", roots, want)
	}
}

func TestPublishStampsPublishedAt(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	gid, err := st.CreateGallery(ctx, model.Gallery{Slug: "g", Title: "G", Type: model.GalleryGrid, Status: model.GalleryDraft})
	if err != nil {
		t.Fatal(err)
	}
	if pa := galleryByID(t, st, gid).PublishedAt; pa != nil {
		t.Fatalf("draft gallery should have no published_at, got %v", pa)
	}

	if err := st.UpdateGalleryStatus(ctx, gid, model.GalleryPublished); err != nil {
		t.Fatal(err)
	}
	first := galleryByID(t, st, gid).PublishedAt
	if first == nil {
		t.Fatal("publishing should stamp published_at")
	}

	// Re-publishing (or any later status change) must preserve the original date.
	if err := st.UpdateGalleryStatus(ctx, gid, model.GalleryUnlisted); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateGalleryStatus(ctx, gid, model.GalleryPublished); err != nil {
		t.Fatal(err)
	}
	again := galleryByID(t, st, gid).PublishedAt
	if again == nil || !again.Equal(*first) {
		t.Errorf("published_at changed on re-publish: %v -> %v", first, again)
	}
}
