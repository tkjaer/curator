package build

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tkjaer/curator/internal/config"
)

func TestSortFacetPhotosNewestFirst(t *testing.T) {
	old := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	newest := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	photos := []facetPhoto{
		{Filename: "z-undated.jpg"},
		{TakenAt: &old, Filename: "old.jpg"},
		{TakenAt: &newest, Filename: "a-new.jpg"},
		{TakenAt: &newest, Filename: "z-new.jpg"},
		{Filename: "a-undated.jpg"},
	}

	sortFacetPhotos(photos)

	want := []string{"z-new.jpg", "a-new.jpg", "old.jpg", "z-undated.jpg", "a-undated.jpg"}
	for i, filename := range want {
		if photos[i].Filename != filename {
			t.Fatalf("photo %d = %q, want %q", i, photos[i].Filename, filename)
		}
	}
}

func TestBrowseValuePagePaths(t *testing.T) {
	cfg := config.New(t.TempDir(), filepath.Join(t.TempDir(), "output"))
	builder := &Builder{Cfg: cfg}
	builder.site.BaseURL = "https://example.com"

	if got := builder.browseValuePageURL("camera", "Leica M11", 1); got != "https://example.com/_curator/browse/camera/leica-m11/" {
		t.Fatalf("page 1 URL = %q", got)
	}
	if got := builder.browseValuePageURL("camera", "Leica M11", 2); got != "https://example.com/_curator/browse/camera/leica-m11/page/2/" {
		t.Fatalf("page 2 URL = %q", got)
	}
	want := filepath.Join(cfg.OutputDir, "_curator", "browse", "camera", "leica-m11", "page", "2", "index.html")
	if got := builder.browseValuePageOutput("camera", "Leica M11", 2); got != want {
		t.Fatalf("page 2 output = %q, want %q", got, want)
	}
}

func TestFacetPaginationSettings(t *testing.T) {
	enabled, pageSize := facetPaginationSettings(nil)
	if !enabled || pageSize != 100 {
		t.Fatalf("defaults = %t, %d", enabled, pageSize)
	}

	enabled, pageSize = facetPaginationSettings(map[string]string{
		"metadata.facet_pagination_enabled": "false",
		"metadata.facet_page_size":          "24",
	})
	if enabled || pageSize != 24 {
		t.Fatalf("configured = %t, %d", enabled, pageSize)
	}
}
