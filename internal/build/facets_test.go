package build

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tkjaer/curator/internal/config"
)

func TestVisibleUserTags(t *testing.T) {
	tags := []string{"Night", "Private", "Stockholm"}
	tests := []struct {
		name     string
		settings map[string]string
		want     []string
	}{
		{name: "show all", settings: map[string]string{"metadata.tag_visibility": "show_all"}, want: tags},
		{name: "default shows all", settings: nil, want: tags},
		{name: "hide all", settings: map[string]string{"metadata.tag_visibility": "hide_all"}, want: []string{}},
		{name: "show selected", settings: map[string]string{"metadata.tag_visibility": "show_selected", "metadata.tag_selection": "night\nSTOCKHOLM"}, want: []string{"Night", "Stockholm"}},
		{name: "hide selected", settings: map[string]string{"metadata.tag_visibility": "hide_selected", "metadata.tag_selection": "PRIVATE"}, want: []string{"Night", "Stockholm"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := visibleUserTags(tags, tt.settings); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("visible tags = %#v, want %#v", got, tt.want)
			}
		})
	}
}

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

	if got := builder.browseValuePageURL("camera", "Leica M11", 1); got != "https://example.com/browse/camera/leica-m11/" {
		t.Fatalf("page 1 URL = %q", got)
	}
	if got := builder.browseValuePageURL("camera", "Leica M11", 2); got != "https://example.com/browse/camera/leica-m11/page/2/" {
		t.Fatalf("page 2 URL = %q", got)
	}
	want := filepath.Join(cfg.OutputDir, "browse", "camera", "leica-m11", "page", "2", "index.html")
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
