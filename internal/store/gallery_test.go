package store

import (
	"context"
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
