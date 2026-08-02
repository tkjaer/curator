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
	for _, gallerySlug := range []string{"_curator", "feed.xml"} {
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
