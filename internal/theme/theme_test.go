package theme

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/tkjaer/curator/internal/render"
)

func loadDefault(t *testing.T) *Theme {
	t.Helper()
	th, err := Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatalf("load default theme: %v", err)
	}
	return th
}

func sampleSite() render.SiteView {
	return render.SiteView{
		Title:   "My Photos",
		BaseURL: "",
		Nav:     []render.NavNode{{Title: "Trips", Href: "/trips/"}},
		Facets:  []render.FacetLink{{Label: "Cameras", Href: "/browse/camera/"}},
	}
}

func samplePhotos() []render.PhotoView {
	mk := func(w, h int, aspect, alt string) render.PhotoView {
		return render.PhotoView{
			Width: w, Height: h, Aspect: aspect, Alt: alt,
			Thumb:   render.Source{URL: "/img/" + alt + "-t.jpg", Width: 400},
			Display: render.Source{URL: "/img/" + alt + "-d.jpg", Width: 1600},
			Srcset: []render.Source{
				{URL: "/img/" + alt + "-800.jpg", Width: 800},
				{URL: "/img/" + alt + "-1600.jpg", Width: 1600},
			},
		}
	}
	return []render.PhotoView{
		mk(3000, 2000, "landscape", "a"),
		mk(2000, 2000, "square", "b"),
		mk(3000, 2000, "landscape", "c"),
	}
}

func TestRenderGalleryGrid(t *testing.T) {
	th := loadDefault(t)
	rows := render.Justify(samplePhotos(), 1000, 300, 8, true)

	view := render.GalleryView{
		Title:   "Spring Trip",
		Type:    "grid",
		Rows:    rows,
		Options: th.Manifest.Defaults(),
		Site:    sampleSite(),
	}

	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery-grid", view); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"Spring Trip", "My Photos", "srcset=", "flex-basis:", "theme.js"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(out, "<no value>") {
		t.Errorf("template produced <no value>:\n%s", out)
	}
}

func TestRenderStoryWithGrid(t *testing.T) {
	th := loadDefault(t)
	rows := render.Justify(samplePhotos(), 900, 260, 8, false)

	view := render.GalleryView{
		Title: "A Day Out",
		Type:  "story",
		Blocks: []render.BlockView{
			{Type: "text", HTML: "<p>Some prose.</p>"},
			{Type: "grid", Rows: rows},
		},
		Options: th.Manifest.Defaults(),
		Site:    sampleSite(),
	}

	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery-story", view); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Some prose.") || !strings.Contains(out, "srcset=") {
		t.Errorf("story output missing expected content:\n%s", out)
	}
}

func TestManifestOptions(t *testing.T) {
	th := loadDefault(t)
	defaults := th.Manifest.Defaults()
	if defaults["panoFullWidth"] != false {
		t.Errorf("panoFullWidth default = %v, want false", defaults["panoFullWidth"])
	}
	if len(th.Manifest.RequiresPresets) == 0 {
		t.Error("expected requiresPresets to be declared")
	}
}

func TestFolioTheme(t *testing.T) {
	th, err := Load(os.DirFS("../../themes/folio"))
	if err != nil {
		t.Fatalf("load folio theme: %v", err)
	}
	if th.Manifest.Name != "folio" {
		t.Fatalf("manifest name = %q, want folio", th.Manifest.Name)
	}
	if _, err := th.Assets(); err != nil {
		t.Fatalf("folio assets: %v", err)
	}

	rows := render.Justify(samplePhotos(), 1000, 340, 12, false)
	views := []struct {
		name string
		view render.GalleryView
	}{
		{"gallery-grid", render.GalleryView{Title: "Modern Grid", Type: "grid", Rows: rows, Options: th.Manifest.Defaults(), Site: sampleSite()}},
		{"gallery-story", render.GalleryView{Title: "Modern Story", Type: "story", Blocks: []render.BlockView{{Type: "text", HTML: "<p>Editorial copy.</p>"}, {Type: "grid", Rows: rows}}, Options: th.Manifest.Defaults(), Site: sampleSite()}},
	}
	for _, test := range views {
		var buf bytes.Buffer
		if err := th.Render(&buf, test.name, test.view); err != nil {
			t.Fatalf("render %s: %v", test.name, err)
		}
		if strings.Contains(buf.String(), "<no value>") {
			t.Errorf("%s produced <no value>", test.name)
		}
	}
}

func TestThemesRenderFacetCards(t *testing.T) {
	for _, name := range []string{"default", "folio"} {
		t.Run(name, func(t *testing.T) {
			th, err := Load(os.DirFS("../../themes/" + name))
			if err != nil {
				t.Fatalf("load theme: %v", err)
			}

			view := render.FacetIndexView{
				Title: "Camera",
				Items: []render.FacetItem{{
					Title: "Example Camera",
					Href:  "/browse/camera/example-camera/",
					Cover: render.Source{URL: "/img/camera.jpg", Width: 800, Height: 533},
					Count: 3,
				}},
				Options: th.Manifest.Defaults(),
				Site:    sampleSite(),
			}

			var buf bytes.Buffer
			if err := th.Render(&buf, "facet-index", view); err != nil {
				t.Fatalf("render facet cards: %v", err)
			}
			if !strings.Contains(buf.String(), "Example Camera") {
				t.Error("facet card title missing from output")
			}
		})
	}
}
