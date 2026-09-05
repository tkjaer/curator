package theme

import (
	"bytes"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

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
		Title:        "My Photos",
		BaseURL:      "",
		AssetVersion: "asset-test",
		Nav:          []render.NavNode{{Title: "Trips", Href: "/trips/"}},
		Facets:       []render.FacetLink{{Label: "Cameras", Href: "/browse/camera/"}},
	}
}

func samplePhotos() []render.PhotoView {
	mk := func(w, h int, aspect, alt string) render.PhotoView {
		return render.PhotoView{
			Width: w, Height: h, Aspect: aspect, Alt: alt,
			Thumb:   render.Source{URL: "/_curator/img/" + alt + "-t.jpg", Width: 400},
			Display: render.Source{URL: "/_curator/img/" + alt + "-d.jpg", Width: 1600},
			Zoom:    render.Source{URL: "/_curator/img/" + alt + "-2400.jpg", Width: 2400},
			Share:   render.Source{URL: "/_curator/img/" + alt + "-800.jpg", Width: 800},
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
	for index := range photos {
		photos[index].ID = int64(index + 1)
	}
	photos[0].Title = "Harbor light"
	photos[0].Description = "Boats at dusk"
	photos[0].Tags = []render.TagView{{Label: "night", Href: "/browse/tag/night/"}, {Label: "stockholm"}}
	return photos
}

func TestRenderGalleryGrid(t *testing.T) {
	th := loadDefault(t)
	rows := render.Justify(samplePhotos(), 1000, 300, 8, true)

	view := render.GalleryView{
		Title:       "Spring Trip",
		Type:        "grid",
		Rows:        rows,
		ShowSharing: true,
		Options:     th.Manifest.Defaults(),
		Site:        sampleSite(),
	}

	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery-grid", view); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"Spring Trip", "My Photos", "srcset=", "flex-basis:", `theme.css?v=asset-test`, `theme.js?v=asset-test`, `href="/browse/camera/"`, `data-title="Harbor light"`, `data-description="Boats at dusk"`, `data-zoom-src="/_curator/img/a-2400.jpg"`, `data-share-src="/_curator/img/a-800.jpg"`, `class="lb-btn lb-share"`, `data-share-action="markdown"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(out, `href="/trips/"`) {
		t.Error("global navigation included gallery links")
	}
	if strings.Contains(out, `class="fig-tags"`) {
		t.Error("grid gallery rendered visible photo tags")
	}
	for _, want := range []string{`class="lightbox-tags-source" hidden`, `href="/browse/tag/night/">night</a>`, `<div class="lb-tags" aria-label="Photo tags"></div>`} {
		if !strings.Contains(out, want) {
			t.Errorf("grid gallery missing lightbox tag source %q", want)
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

func TestRenderStoryImageTags(t *testing.T) {
	th := loadDefault(t)
	photo := samplePhotos()[0]
	view := render.GalleryView{
		Title: "Tagged story", Type: "story",
		Blocks:  []render.BlockView{{Type: "image", Photo: &photo}},
		Options: th.Manifest.Defaults(), Site: sampleSite(),
	}

	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery-story", view); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`href="/browse/tag/night/">night</a>`, `<span>stockholm</span>`} {
		if !strings.Contains(out, want) {
			t.Errorf("story image missing %q", want)
		}
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

func TestManifestResolveOptions(t *testing.T) {
	manifest := Manifest{
		Name: "example",
		Options: []Option{
			{Key: "enabled", Type: "bool", Default: true},
			{Key: "size", Type: "int", Default: float64(10)},
			{Key: "accent", Type: "color", Default: "#fff"},
		},
	}
	options := manifest.ResolveOptions(map[string]string{
		"theme.example.enabled": "false",
		"theme.example.size":    "24",
		"theme.example.accent":  "#123456",
	})
	if options["enabled"] != false || options["size"] != 24 || options["accent"] != "#123456" {
		t.Fatalf("resolved options = %#v", options)
	}
}

func TestThemesRenderZeroGridGap(t *testing.T) {
	for _, name := range []string{"darkroom", "default", "folio", "nordic"} {
		t.Run(name, func(t *testing.T) {
			th, err := Load(os.DirFS("../../themes/" + name))
			if err != nil {
				t.Fatal(err)
			}
			options := th.Manifest.Defaults()
			options["gridGap"] = 0
			view := render.GalleryView{Title: "Zero gap", Options: options, Site: sampleSite()}
			var buf bytes.Buffer
			if err := th.Render(&buf, "gallery-list", view); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), "--gap: 0px;") {
				t.Errorf("%s did not render zero grid gap", name)
			}
		})
	}
}

func TestContentVersionIncludesTemplates(t *testing.T) {
	files := fstest.MapFS{
		"manifest.json":          {Data: []byte(`{"name":"test","version":"1","engine":"go-html-template"}`)},
		"templates/gallery.html": {Data: []byte(`{{define "gallery"}}first{{end}}`)},
		"assets/theme.css":       {Data: []byte(`body { color: black; }`)},
	}

	first, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	firstContent, err := first.ContentVersion()
	if err != nil {
		t.Fatal(err)
	}
	firstAssets, err := first.AssetVersion()
	if err != nil {
		t.Fatal(err)
	}

	files["templates/gallery.html"] = &fstest.MapFile{Data: []byte(`{{define "gallery"}}second{{end}}`)}
	second, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	secondContent, err := second.ContentVersion()
	if err != nil {
		t.Fatal(err)
	}
	secondAssets, err := second.AssetVersion()
	if err != nil {
		t.Fatal(err)
	}

	if firstContent == secondContent {
		t.Fatal("template change did not alter theme content version")
	}
	if firstAssets != secondAssets {
		t.Fatal("template change altered asset-only version")
	}
}

func TestLoadProvidesSharedTemplatesAndAssets(t *testing.T) {
	files := fstest.MapFS{
		"manifest.json":          {Data: []byte(`{"name":"test","version":"1","engine":"go-html-template"}`)},
		"templates/gallery.html": {Data: []byte(`{{define "gallery"}}{{template "grid" .Rows}}{{end}}`)},
		"assets/theme.css":       {Data: []byte(`body { color: black; }`)},
	}
	th, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery", render.GalleryView{}); err != nil {
		t.Fatalf("render shared grid: %v", err)
	}
	assets, err := th.Assets()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile(assets, "theme.js"); err != nil {
		t.Fatalf("shared theme.js missing: %v", err)
	}
	if css, err := fs.ReadFile(assets, "theme.css"); err != nil || !strings.Contains(string(css), "color: black") {
		t.Fatalf("theme CSS missing from merged assets: %v", err)
	}
}

func TestThemeOverridesSharedTemplateAndAsset(t *testing.T) {
	files := fstest.MapFS{
		"manifest.json":               {Data: []byte(`{"name":"test","version":"1","engine":"go-html-template"}`)},
		"templates/gallery.html":      {Data: []byte(`{{define "gallery"}}{{template "nav" .Site}}{{end}}`)},
		"templates/partials/nav.html": {Data: []byte(`{{define "nav"}}custom navigation{{end}}`)},
		"assets/theme.css":            {Data: []byte(`body { color: black; }`)},
		"assets/theme.js":             {Data: []byte(`console.log("custom");`)},
	}

	th, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery", render.GalleryView{}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "custom navigation" {
		t.Fatalf("theme template did not override shared template: %q", buf.String())
	}
	assets, err := th.Assets()
	if err != nil {
		t.Fatal(err)
	}
	js, err := fs.ReadFile(assets, "theme.js")
	if err != nil {
		t.Fatal(err)
	}
	if string(js) != `console.log("custom");` {
		t.Fatalf("theme asset did not override shared asset: %q", js)
	}
}

func TestThemeCanSuppressSharedTemplate(t *testing.T) {
	files := fstest.MapFS{
		"manifest.json":               {Data: []byte(`{"name":"test","version":"1","engine":"go-html-template"}`)},
		"templates/gallery.html":      {Data: []byte(`{{define "gallery"}}before{{template "nav" .Site}}after{{end}}`)},
		"templates/partials/nav.html": {Data: []byte(`{{define "nav"}}{{end}}`)},
		"assets/theme.css":            {Data: []byte(`body { color: black; }`)},
	}
	th, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery", render.GalleryView{}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "beforeafter" {
		t.Fatalf("empty theme override was ignored: %q", buf.String())
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

	photos := samplePhotos()
	rows := render.Justify(photos, 1000, 340, 12, false)
	views := []struct {
		name string
		view render.GalleryView
	}{
		{"gallery-grid", render.GalleryView{Title: "Modern Grid", Type: "grid", Rows: rows, Options: th.Manifest.Defaults(), Site: sampleSite()}},
		{"gallery-story", render.GalleryView{Title: "Modern Story", Type: "story", Blocks: []render.BlockView{{Type: "text", HTML: "<p>Editorial copy.</p>"}, {Type: "image", Photo: &photos[0]}, {Type: "grid", Rows: rows}}, Options: th.Manifest.Defaults(), Site: sampleSite()}},
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
		if test.name == "gallery-grid" && strings.Contains(out, `class="fig-tags"`) {
			t.Errorf("%s rendered visible photo tags", test.name)
		}
		if test.name == "gallery-story" {
			for _, want := range []string{`href="/browse/tag/night/">night</a>`, `<span>stockholm</span>`} {
				if !strings.Contains(out, want) {
					t.Errorf("%s missing %q", test.name, want)
				}
			}
		}
	}
}

func TestFolioGridBackgroundCanBeDisabled(t *testing.T) {
	th, err := Load(os.DirFS("../../themes/folio"))
	if err != nil {
		t.Fatal(err)
	}
	view := render.GalleryView{Title: "Portfolio", Options: th.Manifest.Defaults(), Site: sampleSite()}
	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery-list", view); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `class="has-grid-background"`) {
		t.Error("Folio default lost its grid background")
	}

	view.Options["showGridBackground"] = false
	buf.Reset()
	if err := th.Render(&buf, "gallery-list", view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `class="has-grid-background"`) {
		t.Error("Folio rendered the disabled grid background")
	}
}

func TestDarkroomTheme(t *testing.T) {
	th, err := Load(os.DirFS("../../themes/darkroom"))
	if err != nil {
		t.Fatalf("load darkroom theme: %v", err)
	}
	if th.Manifest.Name != "darkroom" {
		t.Fatalf("manifest name = %q, want darkroom", th.Manifest.Name)
	}
	defaults := th.Manifest.Defaults()
	if defaults["folderBrightness"] != float64(75) || defaults["folderSaturation"] != float64(50) {
		t.Fatalf("darkroom folder image defaults = %#v", defaults)
	}

	photos := samplePhotos()
	view := render.GalleryView{
		Title:       "Outer Hebrides",
		Type:        "grid",
		Hero:        &photos[0],
		Rows:        render.Justify(photos, 1000, 420, 4, true),
		ShowSharing: true,
		Options:     th.Manifest.Defaults(),
		Site:        sampleSite(),
	}

	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery-grid", view); err != nil {
		t.Fatalf("render darkroom: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`class="gallery-hero has-image"`,
		`<h2 class="visually-hidden">Photographs</h2>`,
		`fetchpriority="high"`,
		`data-id=""`,
		`alt="a"`,
		`alt="b"`,
		`class="lb-btn lb-share"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("darkroom output missing %q", want)
		}
	}
	if strings.Count(out, `alt="a"`) != 2 {
		t.Error("darkroom cover should remain in the regular grid")
	}
	for _, want := range []string{"--folder-brightness: 75%;", "--folder-saturation: 50%;"} {
		if !strings.Contains(out, want) {
			t.Errorf("darkroom output missing %q", want)
		}
	}
	if !strings.Contains(out, `data-lightbox-key="1"`) {
		t.Error("darkroom hero and grid are missing stable lightbox keys")
	}

	view.Options["showHero"] = false
	buf.Reset()
	if err := th.Render(&buf, "gallery-grid", view); err != nil {
		t.Fatalf("render darkroom without hero: %v", err)
	}
	out = buf.String()
	if strings.Contains(out, `class="gallery-hero`) || !strings.Contains(out, `class="gallery-heading"`) {
		t.Error("darkroom did not switch to its compact heading")
	}
	if strings.Count(out, `alt="a"`) != 1 {
		t.Error("darkroom without a hero did not restore the first grid photo")
	}
	view.Options["showHero"] = true

	view.Title = view.Site.Title
	view.IsHome = true
	view.Site.Introduction = "Photography by Example Name"
	buf.Reset()
	if err := th.Render(&buf, "gallery-list", view); err != nil {
		t.Fatalf("render darkroom homepage: %v", err)
	}
	out = buf.String()
	for _, want := range []string{`class="gallery-hero has-image site-home"`, `<h1>Photography by Example Name</h1>`, `class="lb-btn lb-close"`, `d="m15 5-7 7 7 7"`} {
		if !strings.Contains(out, want) {
			t.Errorf("darkroom homepage missing %q", want)
		}
	}
	if strings.Contains(out, `<h1>My Photos</h1>`) {
		t.Error("darkroom homepage repeated the visible site title")
	}

	view.Site.Introduction = ""
	buf.Reset()
	if err := th.Render(&buf, "gallery-list", view); err != nil {
		t.Fatalf("render darkroom homepage fallback: %v", err)
	}
	if !strings.Contains(buf.String(), `<h1>My Photos</h1>`) {
		t.Error("darkroom homepage did not fall back to the site title")
	}
}

func TestNordicTheme(t *testing.T) {
	th, err := Load(os.DirFS("../../themes/nordic"))
	if err != nil {
		t.Fatalf("load nordic theme: %v", err)
	}
	if th.Manifest.Name != "nordic" {
		t.Fatalf("manifest name = %q, want nordic", th.Manifest.Name)
	}
	photos := samplePhotos()
	options := th.Manifest.Defaults()
	if options["showHero"] != false {
		t.Fatal("Nordic should default to its compact hero-free layout")
	}
	options["showHero"] = true
	view := render.GalleryView{
		Title: "Outer Hebrides", Type: "grid", Hero: &photos[0],
		Rows:    render.Justify(photos, 1000, 380, 16, true),
		Options: options, Site: sampleSite(),
	}
	var buf bytes.Buffer
	if err := th.Render(&buf, "gallery-grid", view); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`class="gallery-hero has-image"`, `fetchpriority="high"`, `alt="a"`, `alt="b"`} {
		if !strings.Contains(out, want) {
			t.Errorf("Nordic output missing %q", want)
		}
	}
	if strings.Count(out, `alt="a"`) != 2 {
		t.Error("Nordic cover should remain in the regular grid")
	}

	view.Options["showHero"] = false
	buf.Reset()
	if err := th.Render(&buf, "gallery-grid", view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `class="gallery-hero`) || !strings.Contains(buf.String(), `class="gallery-heading"`) {
		t.Error("Nordic did not render its compact hero-free heading")
	}

	assets, err := th.Assets()
	if err != nil {
		t.Fatal(err)
	}
	css, err := fs.ReadFile(assets, "theme.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"@media (prefers-color-scheme: dark)", "@media (prefers-reduced-motion: reduce)", "--accent: #8eb9b5", ".lightbox { padding: .5rem; }"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("Nordic CSS missing %q", want)
		}
	}
}

func TestThemesIncludeLightboxZoomAssets(t *testing.T) {
	for _, name := range []string{"darkroom", "default", "folio", "nordic"} {
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
				"theme.css": {".lightbox:focus { outline: none; }", ".lightbox[open]:not(.is-zoomed)", "place-items: center", ".lightbox.is-zoomed .lb-img", ".lightbox.is-loading:not(.is-opening) .lb-img", ".lightbox.is-opening .lb-img", ".lb-share-panel[hidden]", "cursor: zoom-in", "cursor: zoom-out"},
				"theme.js":  {"function toggleZoom", "function panZoom", "function navigate", "function shareValues", "navigator.clipboard.writeText", "navigator.share", "anchor.outerHTML", "dataset.shareSrc", "gainX", "dataset.zoomSrc", "preload.decode", `classList.add("is-zoomed")`, `classList.add("is-loading")`, `classList.contains("is-loading")`, "request !== imageRequest", `img.removeAttribute("src")`, `addEventListener("pointermove", panZoom)`, `querySelector(".lb-tags")`, "tags.replaceChildren()", `querySelector(".lightbox-tags-source")`, "imageButton.classList.add(\"suppress-focus-ring\");\n    if (document.activeElement === imageButton)"},
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
			if name == "darkroom" {
				content, err := fs.ReadFile(assets, "theme.js")
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(content), `closest("figure, .gallery-hero")`) {
					t.Error("darkroom lightbox does not discover hero tags")
				}
				if !strings.Contains(string(content), `indexByItem.get(key)`) {
					t.Error("darkroom lightbox does not deduplicate the cover hero")
				}
				if !strings.Contains(string(content), `!link.closest(".gallery-hero")`) {
					t.Error("darkroom lightbox does not preserve grid navigation order")
				}
			}
		})
	}
}

func TestThemesPreservePhotoAspectOnMobile(t *testing.T) {
	for _, name := range []string{"darkroom", "default", "folio", "nordic"} {
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
	for _, name := range []string{"darkroom", "default", "folio", "nordic"} {
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
	for _, name := range []string{"darkroom", "default", "folio", "nordic"} {
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
			output := buf.String()
			copyrightAt := strings.Index(output, "© 2025–2026 Example Name")
			if copyrightAt < 0 {
				t.Error("copyright footer missing")
			}
			creditAt := strings.Index(output, `href="https://github.com/tkjaer/curator"`)
			if creditAt < 0 {
				t.Error("Curator repository link missing")
			}
			if creditAt < copyrightAt {
				t.Error("Curator credit should follow copyright")
			}
		})
	}
}

func TestThemesRenderFacetCards(t *testing.T) {
	for _, name := range []string{"darkroom", "default", "folio", "nordic"} {
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

func TestThemesRenderFacetBreadcrumbs(t *testing.T) {
	for _, name := range []string{"darkroom", "default", "folio", "nordic"} {
		t.Run(name, func(t *testing.T) {
			th, err := Load(os.DirFS("../../themes/" + name))
			if err != nil {
				t.Fatal(err)
			}
			view := render.FacetValueView{
				Title:      "5 September 2025",
				Breadcrumb: []render.Crumb{{Title: "Date", Href: "/browse/date/"}, {Title: "2025", Href: "/browse/date/2025/"}},
				Page:       1,
				PageCount:  1,
				Options:    th.Manifest.Defaults(),
				Site:       sampleSite(),
			}
			var buf bytes.Buffer
			if err := th.Render(&buf, "facet-value", view); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{`href="/browse/date/"`, `href="/browse/date/2025/"`} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("%s date page missing breadcrumb %q", name, want)
				}
			}
		})
	}
}

func TestEditorialThemesRenderSingularPhotoCount(t *testing.T) {
	for _, name := range []string{"darkroom", "folio"} {
		t.Run(name, func(t *testing.T) {
			th, err := Load(os.DirFS("../../themes/" + name))
			if err != nil {
				t.Fatal(err)
			}
			view := render.FacetIndexView{
				Title: "Date",
				Items: []render.FacetItem{{
					Title: "2024", Href: "/browse/date/2024/", Count: 1,
				}},
				Options: th.Manifest.Defaults(),
				Site:    sampleSite(),
			}
			var buf bytes.Buffer
			if err := th.Render(&buf, "facet-index", view); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), "1 photo") || strings.Contains(buf.String(), "1 photos") {
				t.Errorf("%s rendered an incorrect singular photo count", name)
			}
		})
	}
}
