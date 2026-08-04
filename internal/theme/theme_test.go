package theme

import (
	"bytes"
	"io/fs"
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
			Thumb:   render.Source{URL: "/_curator/img/" + alt + "-t.jpg", Width: 400},
			Display: render.Source{URL: "/_curator/img/" + alt + "-d.jpg", Width: 1600},
			Zoom:    render.Source{URL: "/_curator/img/" + alt + "-2400.jpg", Width: 2400},
			Srcset: []render.Source{
				{URL: "/_curator/img/" + alt + "-800.jpg", Width: 800},
				{URL: "/_curator/img/" + alt + "-1600.jpg", Width: 1600},
				{URL: "/_curator/img/" + alt + "-2400.jpg", Width: 2400},
			},
		}
	}
	photos := []render.PhotoView{
		mk(3000, 2000, "landscape", "a"),
		mk(2000, 2000, "square", "b"),
		mk(3000, 2000, "landscape", "c"),
	}
	photos[0].Title = "Harbor light"
	photos[0].Description = "Boats at dusk"
	return photos
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

	for _, want := range []string{"Spring Trip", "My Photos", "srcset=", "flex-basis:", "theme.js", `href="/browse/camera/"`, `data-title="Harbor light"`, `data-description="Boats at dusk"`, `data-zoom-src="/_curator/img/a-2400.jpg"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(out, `href="/trips/"`) {
		t.Error("global navigation included gallery links")
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
		out := buf.String()
		if strings.Contains(out, "<no value>") {
			t.Errorf("%s produced <no value>", test.name)
		}
		for _, want := range []string{`data-zoom-src="/_curator/img/a-2400.jpg"`, `class="lb-image-button"`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s missing %q", test.name, want)
			}
		}
	}
}

func TestThemesIncludeLightboxZoomAssets(t *testing.T) {
	for _, name := range []string{"default", "folio"} {
		t.Run(name, func(t *testing.T) {
			th, err := Load(os.DirFS("../../themes/" + name))
			if err != nil {
				t.Fatal(err)
			}
			assets, err := th.Assets()
			if err != nil {
				t.Fatal(err)
			}
			for file, wants := range map[string][]string{
				"theme.css": {".lightbox.is-zoomed .lb-img", "cursor: zoom-in", "cursor: zoom-out"},
				"theme.js":  {"function toggleZoom", "dataset.zoomSrc", `classList.add("is-zoomed")`},
			} {
				content, err := fs.ReadFile(assets, file)
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range wants {
					if !strings.Contains(string(content), want) {
						t.Errorf("%s missing %q", file, want)
					}
				}
			}
		})
	}
}

func TestThemesPreservePhotoAspectOnMobile(t *testing.T) {
	for _, name := range []string{"default", "folio"} {
		t.Run(name, func(t *testing.T) {
			th, err := Load(os.DirFS("../../themes/" + name))
			if err != nil {
				t.Fatal(err)
			}
			assets, err := th.Assets()
			if err != nil {
				t.Fatal(err)
			}
			css, err := fs.ReadFile(assets, "theme.css")
			if err != nil {
				t.Fatal(err)
			}
			for _, rule := range []string{
				"(orientation: landscape) and (max-height: 500px)",
				".row { display: flex; align-items: flex-start;",
				".photo a { display: block; height: auto;",
				".photo img {",
				"height: auto;",
				"object-fit: contain;",
				"max-height: calc(100dvh - 1rem);",
			} {
				if !strings.Contains(string(css), rule) {
					t.Errorf("mobile gallery CSS missing %q", rule)
				}
			}
		})
	}
}

func TestFolioHomepageOmitsRepeatedSiteTitle(t *testing.T) {
	th, err := Load(os.DirFS("../../themes/folio"))
	if err != nil {
		t.Fatalf("load folio theme: %v", err)
	}
	site := sampleSite()
	view := render.GalleryView{Title: site.Title, Options: th.Manifest.Defaults(), Site: site}

	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery-list", view); err != nil {
		t.Fatalf("render homepage: %v", err)
	}
	if !strings.Contains(buf.String(), `<h1 class="visually-hidden">My Photos</h1>`) {
		t.Error("homepage repeated the site title as a page heading")
	}
	if !strings.Contains(buf.String(), "<title>My Photos</title>") {
		t.Error("homepage repeated the site name in the document title")
	}

	view.Title = "Trips"
	view.Breadcrumb = []render.Crumb{{Title: "Trips", Href: "/trips/"}}
	buf.Reset()
	if err := th.Render(&buf, "gallery-list", view); err != nil {
		t.Fatalf("render folder: %v", err)
	}
	if !strings.Contains(buf.String(), `<h1 class="visually-hidden">Trips</h1>`) {
		t.Error("folder gallery lost its semantic page heading")
	}
}

func TestFolioGalleryTitlesAreVisuallyHidden(t *testing.T) {
	th, err := Load(os.DirFS("../../themes/folio"))
	if err != nil {
		t.Fatalf("load folio theme: %v", err)
	}
	view := render.GalleryView{Title: "Summer", Options: th.Manifest.Defaults(), Site: sampleSite()}

	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery-grid", view); err != nil {
		t.Fatalf("render grid: %v", err)
	}
	if !strings.Contains(buf.String(), `<h1 class="visually-hidden">Summer</h1>`) {
		t.Error("grid gallery title should be visually hidden")
	}

	buf.Reset()
	if err := th.Render(&buf, "gallery-story", view); err != nil {
		t.Fatalf("render story: %v", err)
	}
	if !strings.Contains(buf.String(), `<h1 class="visually-hidden">Summer</h1>`) {
		t.Error("story gallery title should be visually hidden")
	}
}

func TestThemesShowEXIFOnlyInLightbox(t *testing.T) {
	for _, name := range []string{"default", "folio"} {
		t.Run(name, func(t *testing.T) {
			th, err := Load(os.DirFS("../../themes/" + name))
			if err != nil {
				t.Fatalf("load theme: %v", err)
			}
			photos := samplePhotos()
			photos[0].Caption = "Visible caption"
			photos[0].Exif = &render.ExifView{Camera: "Example Camera", ISO: "200"}
			rows := render.Justify(photos, 1000, 300, 8, false)
			view := render.GalleryView{
				Title: "EXIF gallery", Type: "grid", Rows: rows,
				Options: th.Manifest.Defaults(), Site: sampleSite(),
			}

			var buf bytes.Buffer
			if err := th.Render(&buf, "gallery-grid", view); err != nil {
				t.Fatalf("render: %v", err)
			}
			out := buf.String()
			if !strings.Contains(out, `data-exif="Example Camera · ISO 200"`) {
				t.Error("lightbox EXIF data missing")
			}
			if !strings.Contains(out, `<span class="fig-caption">Visible caption</span>`) {
				t.Error("grid caption missing")
			}
			if strings.Contains(out, `class="fig-exif"`) {
				t.Error("EXIF should not be visible in the grid")
			}
		})
	}
}

func TestThemesRenderCopyrightFooter(t *testing.T) {
	for _, name := range []string{"default", "folio"} {
		t.Run(name, func(t *testing.T) {
			th, err := Load(os.DirFS("../../themes/" + name))
			if err != nil {
				t.Fatalf("load theme: %v", err)
			}
			site := sampleSite()
			site.Copyright = "© 2025–2026 Example Name"
			view := render.GalleryView{
				Title: "Copyright gallery", Type: "grid",
				Options: th.Manifest.Defaults(), Site: site,
			}

			var buf bytes.Buffer
			if err := th.Render(&buf, "gallery-grid", view); err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(buf.String(), "© 2025–2026 Example Name") {
				t.Error("copyright footer missing")
			}
		})
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
					Cover: render.Source{URL: "/_curator/img/camera.jpg", Width: 800, Height: 533},
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
