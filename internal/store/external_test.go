package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/tkjaer/curator/internal/model"
)

func TestExternalMappings(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	galleryID, err := st.CreateGallery(ctx, model.Gallery{Slug: "lightroom", Title: "Lightroom"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetExternalGallery(ctx, "lightroom", "collection-7", galleryID); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.ExternalGalleryID(ctx, "lightroom", "collection-7")
	if err != nil || !found || got != galleryID {
		t.Fatalf("ExternalGalleryID() = %d, %v, %v", got, found, err)
	}

	if _, found, err := st.ExternalGalleryID(ctx, "lightroom", "missing"); err != nil || found {
		t.Fatalf("missing ExternalGalleryID() found = %v, err = %v", found, err)
	}
}

func TestExternalGalleryCreationAppendsAfterCustomSiblingOrder(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	var ids []int64
	for _, gallerySlug := range []string{"first", "second", "third"} {
		id, created, err := st.UpsertExternalGallery(ctx, "lightroom", gallerySlug, model.Gallery{
			Slug: gallerySlug, Title: gallerySlug, Type: model.GalleryGrid,
			Status: model.GalleryDraft, SortMode: model.SortDefault,
		})
		if err != nil || !created {
			t.Fatalf("create %q = %d, %v, %v", gallerySlug, id, created, err)
		}
		ids = append(ids, id)
	}
	if err := st.MoveGalleryOrder(ctx, ids[2], true); err != nil {
		t.Fatal(err)
	}
	lastID, created, err := st.UpsertExternalGallery(ctx, "lightroom", "last", model.Gallery{
		Slug: "last", Title: "last", Type: model.GalleryGrid,
		Status: model.GalleryDraft, SortMode: model.SortDefault,
	})
	if err != nil || !created {
		t.Fatalf("create last = %d, %v, %v", lastID, created, err)
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
		t.Errorf("external gallery order after insert = %v, want %v", got, want)
	}
}
