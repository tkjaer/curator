package store

import (
	"context"
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
