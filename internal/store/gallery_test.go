package store

import (
	"context"
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
