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
		want          string
	}{
		{"Lightroom tag overrides EXIF", exif.Data{Camera: "FUJIFILM XF10", Lens: "Embedded", XMPLens: "Profile"}, "Tagged lens", "Tagged lens"},
		{"EXIF before sidecar", exif.Data{Camera: "FUJIFILM XF10", Lens: "Embedded", SidecarLens: "Sidecar lens"}, "", "Embedded"},
		{"Lightroom tag before sidecar", exif.Data{Camera: "FUJIFILM XF10", SidecarLens: "Sidecar lens", XMPLens: "Profile"}, "Tagged lens", "Tagged lens"},
		{"sidecar before mapping", exif.Data{Camera: "FUJIFILM XF10", SidecarLens: "Manual 18mm", XMPLens: "Profile"}, "", "Manual 18mm"},
		{"mapping before profile", exif.Data{Camera: "FUJIFILM XF10", XMPLens: "Profile"}, "", "FUJINON 18.5mm F2.8"},
		{"profile fallback", exif.Data{Camera: "FUJIFILM GFX 50R", XMPLens: "Voigtlander 15mm"}, "", "Voigtlander 15mm"},
	}
	for _, test := range tests {
		if got := policy.Resolve(test.meta.Camera, test.meta.Lens, test.lightroomLens, test.meta.SidecarLens, test.meta.XMPLens); got != test.want {
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
	sidecar := `<rdf:Description xmlns:rdf="urn:rdf" xmlns:aux="http://ns.adobe.com/exif/1.0/aux/" aux:Lens="Voigtlander 15mm f/4.5"/>`
	if err := ImportUploadWithSidecar(ctx, st, cfg, galleryID, "manual", "photo.jpg", &imageData, bytes.NewBufferString(sidecar)); err != nil {
		t.Fatal(err)
	}

	items, err := st.ItemsByGallery(ctx, galleryID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SidecarLens != "Voigtlander 15mm f/4.5" || items[0].Lens != "Voigtlander 15mm f/4.5" {
		t.Fatalf("imported item = %+v", items)
	}
	storedSidecar, err := os.ReadFile(filepath.Join(cfg.OriginalsDir(), "manual", "photo.xmp"))
	if err != nil {
		t.Fatalf("stored sidecar: %v", err)
	}
	if string(storedSidecar) != sidecar {
		t.Fatal("stored sidecar was modified during import")
	}
}

func TestParseLensMappingsRejectsInvalidLine(t *testing.T) {
	if _, err := ParseLensMappings("FUJIFILM XF10"); err == nil {
		t.Fatal("expected invalid mapping error")
	}
}
