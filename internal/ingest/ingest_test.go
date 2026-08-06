package ingest

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/exif"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/store"
)

func TestLensPolicy(t *testing.T) {
	policy, err := LensPolicyFromSettings(map[string]string{
		"metadata.use_lightroom_lens_profile": "true",
		"metadata.lens_mappings":              "FUJIFILM XF10 = FUJINON 18.5mm F2.8\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		meta          exif.Data
		lightroomLens string
		manualLens    string
		want          string
	}{
		{"Manual override wins", exif.Data{Camera: "FUJIFILM XF10", Lens: "Embedded", SidecarLens: "Sidecar lens", XMPLens: "Profile"}, "Tagged lens", "Curator override", "Curator override"},
		{"Lightroom tag overrides EXIF", exif.Data{Camera: "FUJIFILM XF10", Lens: "Embedded", XMPLens: "Profile"}, "Tagged lens", "", "Tagged lens"},
		{"EXIF before sidecar", exif.Data{Camera: "FUJIFILM XF10", Lens: "Embedded", SidecarLens: "Sidecar lens"}, "", "", "Embedded"},
		{"Lightroom tag before sidecar", exif.Data{Camera: "FUJIFILM XF10", SidecarLens: "Sidecar lens", XMPLens: "Profile"}, "Tagged lens", "", "Tagged lens"},
		{"sidecar before mapping", exif.Data{Camera: "FUJIFILM XF10", SidecarLens: "Manual 18mm", XMPLens: "Profile"}, "", "", "Manual 18mm"},
		{"mapping before profile", exif.Data{Camera: "FUJIFILM XF10", XMPLens: "Profile"}, "", "", "FUJINON 18.5mm F2.8"},
		{"profile fallback", exif.Data{Camera: "FUJIFILM GFX 50R", XMPLens: "Voigtlander 15mm"}, "", "", "Voigtlander 15mm"},
	}
	for _, test := range tests {
		if got := policy.Resolve(test.meta.Camera, test.meta.Lens, test.lightroomLens, test.meta.SidecarLens, test.meta.XMPLens, test.manualLens); got != test.want {
			t.Errorf("%s: lens = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestImportUploadWithSidecar(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.New(tmp, filepath.Join(tmp, "output"))
	ctx := context.Background()
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	galleryID, err := st.CreateGallery(ctx, model.Gallery{Slug: "manual", Title: "Manual"})
	if err != nil {
		t.Fatal(err)
	}

	var imageData bytes.Buffer
	if err := jpeg.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 10, 10)), nil); err != nil {
		t.Fatal(err)
	}
	sidecar := `<rdf:Description xmlns:rdf="urn:rdf" xmlns:aux="http://ns.adobe.com/exif/1.0/aux/" xmlns:dc="http://purl.org/dc/elements/1.1/" aux:Lens="Voigtlander 15mm f/4.5" dc:title="Flickr title" dc:description="Flickr description"/>`
	if err := ImportUploadWithSidecar(ctx, st, cfg, galleryID, "manual", "photo.jpg", &imageData, bytes.NewBufferString(sidecar)); err != nil {
		t.Fatal(err)
	}

	items, err := st.ItemsByGallery(ctx, galleryID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SidecarLens != "Voigtlander 15mm f/4.5" || items[0].Lens != "Voigtlander 15mm f/4.5" ||
		items[0].Title != "Flickr title" || items[0].Description != "Flickr description" {
		t.Fatalf("imported item = %+v", items)
	}
	storedSidecar, err := os.ReadFile(filepath.Join(cfg.OriginalsDir(), "manual", "photo.xmp"))
	if err != nil {
		t.Fatalf("stored sidecar: %v", err)
	}
	if string(storedSidecar) != sidecar {
		t.Fatal("stored sidecar was modified during import")
	}

	it := items[0]
	if err := st.UpdateItemPresentation(ctx, it.ID, it.Title, it.Description, it.Caption, it.Status, it.Highlighted, "Leica M6", "Leica M6", "Manual override", "Manual override"); err != nil {
		t.Fatal(err)
	}
	if updated, skipped, err := Rescan(ctx, st, cfg); err != nil || updated != 1 || skipped != 0 {
		t.Fatalf("rescan = %d updated, %d skipped, %v", updated, skipped, err)
	}
	it, err = st.Item(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if it.ManualCamera != "Leica M6" || it.Camera != "Leica M6" || it.ManualLens != "Manual override" || it.Lens != "Manual override" {
		t.Fatalf("rescan lost manual metadata: %+v", it)
	}

	var replacement bytes.Buffer
	if err := jpeg.Encode(&replacement, image.NewRGBA(image.Rect(0, 0, 12, 12)), nil); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceUploadWithSidecar(ctx, st, cfg, it.ID, galleryID, "manual", "photo.jpg", &replacement, nil); err != nil {
		t.Fatal(err)
	}
	it, err = st.Item(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if it.ManualCamera != "Leica M6" || it.Camera != "Leica M6" || it.ManualLens != "Manual override" || it.Lens != "Manual override" {
		t.Fatalf("replacement lost manual metadata: %+v", it)
	}
}

func TestRescanCorrectsImageDimensionsAndAspect(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.New(tmp, filepath.Join(tmp, "output"))
	ctx := context.Background()
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	galleryID, err := st.CreateGallery(ctx, model.Gallery{Slug: "gallery", Title: "Gallery"})
	if err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join("gallery", "portrait.jpg")
	fullPath := filepath.Join(cfg.OriginalsDir(), originalPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 10, 20)), nil); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	itemID, err := st.CreateItem(ctx, model.Item{
		GalleryID: galleryID, OriginalPath: originalPath, Filename: "portrait.jpg",
		Width: 20, Height: 10, Aspect: model.AspectLandscape, Status: model.ItemPublished,
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, skipped, err := Rescan(ctx, st, cfg)
	if err != nil || updated != 1 || skipped != 0 {
		t.Fatalf("rescan = %d updated, %d skipped, %v", updated, skipped, err)
	}
	item, err := st.Item(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Width != 10 || item.Height != 20 || item.Aspect != model.AspectPortrait {
		t.Fatalf("rescanned geometry = %dx%d %q, want 10x20 portrait", item.Width, item.Height, item.Aspect)
	}
}

func TestResolveCamera(t *testing.T) {
	if got := ResolveCamera("Frontier", "Leica M6"); got != "Leica M6" {
		t.Fatalf("manual camera = %q, want Leica M6", got)
	}
	if got := ResolveCamera("Frontier", ""); got != "Frontier" {
		t.Fatalf("imported camera = %q, want Frontier", got)
	}
}

func TestParseLensMappingsRejectsInvalidLine(t *testing.T) {
	if _, err := ParseLensMappings("FUJIFILM XF10"); err == nil {
		t.Fatal("expected invalid mapping error")
	}
}
