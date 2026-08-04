package build

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/imaging"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/render"
	"github.com/tkjaer/curator/internal/store"
	"github.com/tkjaer/curator/internal/theme"
)

func TestCardCoverUsesResponsiveSource(t *testing.T) {
	photo := render.PhotoView{
		Thumb: render.Source{URL: "/thumb.jpg", Width: 400},
		Srcset: []render.Source{
			{URL: "/wide.jpg", Width: 1600},
			{URL: "/card.jpg", Width: 800},
		},
	}
	if got := cardCover(photo); got.URL != "/card.jpg" {
		t.Fatalf("card cover = %q, want 800px source", got.URL)
	}
	photo.Srcset = nil
	if got := cardCover(photo); got.URL != "/thumb.jpg" {
		t.Fatalf("fallback card cover = %q, want thumbnail", got.URL)
	}
}

func TestBuildProducesSite(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.New(tmp, filepath.Join(tmp, "output"))

	// A source image in the originals directory.
	origRel := filepath.Join("trip", "a.jpg")
	origAbs := filepath.Join(cfg.OriginalsDir(), origRel)
	if err := os.MkdirAll(filepath.Dir(origAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	src := image.NewRGBA(image.Rect(0, 0, 1500, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 1500; x++ {
			src.Set(x, y, color.RGBA{uint8(x), uint8(y), 100, 255})
		}
	}
	if err := imaging.SaveJPEG(origAbs, src, 85); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	gid, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "trip", Title: "Trip", Type: model.GalleryGrid, Status: model.GalleryPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateItem(ctx, model.Item{
		GalleryID: gid, OriginalPath: origRel, Filename: "a.jpg",
		Width: 1500, Height: 1000, Aspect: model.AspectLandscape, Status: model.ItemPublished,
		Title: "Visible title", Description: "Visible description",
	}); err != nil {
		t.Fatal(err)
	}

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatalf("build: %v", err)
	}
	galleryPage := filepath.Join(cfg.OutputDir, "trip", "index.html")
	page, err := os.ReadFile(galleryPage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Visible title") || !strings.Contains(string(page), "Visible description") {
		t.Fatal("inherited title and description defaults were not rendered")
	}
	if err := st.SetSetting(ctx, "site.default_gallery_show_title", "false"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "site.default_gallery_show_description", "false"); err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatalf("rebuild with hidden metadata: %v", err)
	}
	page, err = os.ReadFile(galleryPage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "Visible title") || strings.Contains(string(page), "Visible description") {
		t.Fatal("hidden inherited title or description was rendered")
	}
	if err := st.UpdateGalleryPresentation(ctx, gid, model.VisibilityInherit, model.VisibilityShow, model.VisibilityInherit); err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatalf("rebuild with title override: %v", err)
	}
	page, err = os.ReadFile(galleryPage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Visible title") || strings.Contains(string(page), "Visible description") {
		t.Fatal("gallery title override did not supersede inherited defaults")
	}

	mustExist(t, filepath.Join(cfg.OutputDir, "index.html"))
	mustExist(t, galleryPage)
	mustExist(t, filepath.Join(cfg.OutputDir, "_curator", "assets", "theme.css"))

	imgs, _ := filepath.Glob(filepath.Join(cfg.OutputDir, "_curator", "img", "*.jpg"))
	if len(imgs) == 0 {
		t.Error("no image derivatives were generated")
	}

	derivs, err := st.DerivativesByItem(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(derivs) == 0 {
		t.Error("no derivatives recorded in the database")
	}
}

func TestBuildAppliesLensPolicyWithoutRescan(t *testing.T) {
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
	if err := st.SetFacetEnabled(ctx, "lens", true); err != nil {
		t.Fatal(err)
	}

	gid, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "trip", Title: "Trip", Type: model.GalleryGrid, Status: model.GalleryPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), "trip", "a.jpg"), 1)
	if _, err := st.CreateItem(ctx, model.Item{
		GalleryID: gid, OriginalPath: filepath.Join("trip", "a.jpg"), Filename: "a.jpg",
		Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: model.ItemPublished,
		Camera: "FUJIFILM GFX 50R", XMPLens: "Voigtlander 15mm",
	}); err != nil {
		t.Fatal(err)
	}

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	build := func() {
		t.Helper()
		if err := New(st, th, cfg).Build(ctx); err != nil {
			t.Fatal(err)
		}
	}
	xmpPage := filepath.Join(cfg.OutputDir, "browse", "lens", "voigtlander-15mm", "index.html")
	mappedPage := filepath.Join(cfg.OutputDir, "browse", "lens", "mapped-15mm", "index.html")

	build()
	mustNotExist(t, xmpPage)

	if err := st.SetSetting(ctx, "metadata.use_lightroom_lens_profile", "true"); err != nil {
		t.Fatal(err)
	}
	build()
	mustExist(t, xmpPage)

	if err := st.SetSetting(ctx, "metadata.use_lightroom_lens_profile", "false"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "metadata.lens_mappings", "FUJIFILM GFX 50R = Mapped 15mm"); err != nil {
		t.Fatal(err)
	}
	build()
	mustNotExist(t, xmpPage)
	mustExist(t, mappedPage)
}

func TestCopyrightLine(t *testing.T) {
	settings := map[string]string{
		"site.copyright_holder":     "Example Name",
		"site.copyright_start_year": "2025",
	}
	if got := copyrightLine(settings, 2026); got != "© 2025–2026 Example Name" {
		t.Fatalf("copyright line = %q", got)
	}
	settings["site.copyright_start_year"] = "2026"
	if got := copyrightLine(settings, 2026); got != "© 2026 Example Name" {
		t.Fatalf("single-year copyright line = %q", got)
	}
}

func TestBuildUnlistedIsBuiltButNotLinked(t *testing.T) {
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

	mk := func(slug, title string, status model.GalleryStatus, seed int) {
		gid, err := st.CreateGallery(ctx, model.Gallery{Slug: slug, Title: title, Type: model.GalleryGrid, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), slug, "p.jpg"), seed)
		if _, err := st.CreateItem(ctx, model.Item{
			GalleryID: gid, OriginalPath: filepath.Join(slug, "p.jpg"), Filename: "p.jpg",
			Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: model.ItemPublished,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("shown", "Shown", model.GalleryPublished, 1)
	mk("hidden", "Hidden", model.GalleryUnlisted, 2)

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatal(err)
	}

	mustExist(t, filepath.Join(cfg.OutputDir, "hidden", "index.html"))

	index, err := os.ReadFile(filepath.Join(cfg.OutputDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "Shown") {
		t.Error("published gallery should be linked from the index")
	}
	if strings.Contains(string(index), "hidden") {
		t.Error("unlisted gallery must not be linked from the index")
	}
}

func TestBuildRendersStory(t *testing.T) {
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

	gid, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "story", Title: "Story", Type: model.GalleryStory, Status: model.GalleryPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	var itemIDs []int64
	for _, name := range []string{"a.jpg", "b.jpg"} {
		writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), "story", name), len(itemIDs)+1)
		id, err := st.CreateItem(ctx, model.Item{
			GalleryID: gid, OriginalPath: filepath.Join("story", name), Filename: name,
			Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: model.ItemPublished,
		})
		if err != nil {
			t.Fatal(err)
		}
		itemIDs = append(itemIDs, id)
	}

	if _, err := st.CreateBlock(ctx, model.Block{GalleryID: gid, Type: model.BlockText, Content: "Some **bold** text."}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateBlock(ctx, model.Block{GalleryID: gid, Type: model.BlockImage, ItemID: &itemIDs[0]}); err != nil {
		t.Fatal(err)
	}
	gridID, err := st.CreateBlock(ctx, model.Block{GalleryID: gid, Type: model.BlockGrid})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetBlockItems(ctx, gridID, itemIDs); err != nil {
		t.Fatal(err)
	}

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatalf("build: %v", err)
	}

	html, err := os.ReadFile(filepath.Join(cfg.OutputDir, "story", "index.html"))
	if err != nil {
		t.Fatalf("story page not written: %v", err)
	}
	out := string(html)
	for _, want := range []string{"<strong>bold</strong>", "story-image", `class="row"`} {
		if !strings.Contains(out, want) {
			t.Errorf("story html missing %q", want)
		}
	}
}

func TestBuildFillsDirectoryIndexes(t *testing.T) {
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

	// A draft parent with an unlisted child: the parent's directory exists only
	// because the child is built, and must not be an open listing.
	pid, err := st.CreateGallery(ctx, model.Gallery{Slug: "2026", Title: "2026", Type: model.GalleryGrid, Status: model.GalleryDraft})
	if err != nil {
		t.Fatal(err)
	}
	cid, err := st.CreateGallery(ctx, model.Gallery{Slug: "hidden", Title: "Hidden", Type: model.GalleryGrid, Status: model.GalleryUnlisted, ParentID: &pid})
	if err != nil {
		t.Fatal(err)
	}
	writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), "2026", "hidden", "p.jpg"), 3)
	if _, err := st.CreateItem(ctx, model.Item{
		GalleryID: cid, OriginalPath: filepath.Join("2026", "hidden", "p.jpg"), Filename: "p.jpg",
		Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: model.ItemPublished,
	}); err != nil {
		t.Fatal(err)
	}

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatal(err)
	}

	// Container and intermediate directories must all have an index.html.
	for _, dir := range []string{"_curator", filepath.Join("_curator", "img"), "2026"} {
		mustExist(t, filepath.Join(cfg.OutputDir, dir, "index.html"))
	}
	// The draft parent's placeholder must not reveal the hidden child.
	parent, err := os.ReadFile(filepath.Join(cfg.OutputDir, "2026", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(parent), "Hidden") {
		t.Error("placeholder leaked the unlisted child")
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s not to exist, got %v", path, err)
	}
}

func TestNestedCoverFallback(t *testing.T) {
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

	// A folder gallery with no images of its own, containing a published child
	// that does have an image.
	parent, err := st.CreateGallery(ctx, model.Gallery{Slug: "2026", Title: "2026", Type: model.GalleryGrid, Status: model.GalleryPublished})
	if err != nil {
		t.Fatal(err)
	}
	child, err := st.CreateGallery(ctx, model.Gallery{Slug: "trip", Title: "Trip", Type: model.GalleryGrid, Status: model.GalleryPublished, ParentID: &parent})
	if err != nil {
		t.Fatal(err)
	}
	writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), "2026", "trip", "p.jpg"), 4)
	if _, err := st.CreateItem(ctx, model.Item{
		GalleryID: child, OriginalPath: filepath.Join("2026", "trip", "p.jpg"), Filename: "p.jpg",
		Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: model.ItemPublished,
	}); err != nil {
		t.Fatal(err)
	}

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatal(err)
	}

	index, err := os.ReadFile(filepath.Join(cfg.OutputDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "background-image") {
		t.Error("folder gallery card should inherit a cover from its nested child")
	}
}

func writeSourceImage(t *testing.T, path string, seed int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 600, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 600; x++ {
			img.Set(x, y, color.RGBA{uint8(x + seed), uint8(y + seed), uint8(seed), 255})
		}
	}
	if err := imaging.SaveJPEG(path, img, 80); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSweepsOrphanedDerivatives(t *testing.T) {
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

	gid, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "trip", Title: "Trip", Type: model.GalleryGrid, Status: model.GalleryPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	var itemIDs []int64
	for i, name := range []string{"a.jpg", "b.jpg"} {
		writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), "trip", name), i+1)
		id, err := st.CreateItem(ctx, model.Item{
			GalleryID: gid, OriginalPath: filepath.Join("trip", name), Filename: name,
			Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: model.ItemPublished,
		})
		if err != nil {
			t.Fatal(err)
		}
		itemIDs = append(itemIDs, id)
	}

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}

	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := filepath.Glob(filepath.Join(cfg.OutputDir, "_curator", "img", "*.jpg"))

	// Remove one item and rebuild; its derivatives should be swept away.
	if err := st.DeleteItem(ctx, itemIDs[0]); err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := filepath.Glob(filepath.Join(cfg.OutputDir, "_curator", "img", "*.jpg"))

	if len(after) >= len(before) {
		t.Errorf("expected fewer derivatives after deletion: before=%d after=%d", len(before), len(after))
	}
	if len(after) == 0 {
		t.Error("remaining item's derivatives were swept incorrectly")
	}
}

func TestBuildEmitsNginxAuth(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.New(tmp, filepath.Join(tmp, "output"))
	writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), "secret", "a.jpg"), 7)

	ctx := context.Background()
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "site.server_root", "/srv/site"); err != nil {
		t.Fatal(err)
	}

	gid, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "secret", Title: "Secret", Type: model.GalleryGrid, Status: model.GalleryProtected,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateItem(ctx, model.Item{
		GalleryID: gid, OriginalPath: filepath.Join("secret", "a.jpg"), Filename: "a.jpg",
		Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: model.ItemPublished,
	}); err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateAccessUser(ctx, "bob", "$apr1$abc$def")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetGalleryAccess(ctx, gid, []int64{uid}); err != nil {
		t.Fatal(err)
	}

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatalf("build: %v", err)
	}

	conf, err := os.ReadFile(filepath.Join(cfg.OutputDir, "curator-auth.conf"))
	if err != nil {
		t.Fatalf("expected curator-auth.conf: %v", err)
	}
	for _, want := range []string{"location /secret/", "auth_basic_user_file /srv/site/secret/.htpasswd"} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("curator-auth.conf missing %q\n%s", want, conf)
		}
	}

	htp, err := os.ReadFile(filepath.Join(cfg.OutputDir, "secret", ".htpasswd"))
	if err != nil {
		t.Fatalf("expected .htpasswd: %v", err)
	}
	if !strings.HasPrefix(string(htp), "bob:$apr1$") {
		t.Errorf(".htpasswd content = %q", htp)
	}

	// Protected derivatives must live under the auth-guarded gallery path, not
	// in the shared /_curator/img pool.
	protImgs, _ := filepath.Glob(filepath.Join(cfg.OutputDir, "secret", "img", "*.jpg"))
	if len(protImgs) == 0 {
		t.Error("protected gallery derivatives were not written under its path")
	}
	shared, _ := filepath.Glob(filepath.Join(cfg.OutputDir, "_curator", "img", "*.jpg"))
	if len(shared) != 0 {
		t.Errorf("protected gallery leaked %d derivative(s) into shared /_curator/img", len(shared))
	}
}
