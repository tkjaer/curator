package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestGalleryHeroSelection(t *testing.T) {
	first := render.PhotoView{Slug: "first"}
	second := render.PhotoView{Slug: "second"}
	photos := []render.PhotoView{first, second}
	items := map[int64]render.PhotoView{10: first, 20: second}

	hero, ok := galleryHero(model.Gallery{}, photos, items)
	if !ok || hero.Slug != "first" {
		t.Fatalf("default hero = %q, %t; want first, true", hero.Slug, ok)
	}

	coverID := int64(20)
	hero, ok = galleryHero(model.Gallery{CoverItemID: &coverID}, photos, items)
	if !ok || hero.Slug != "second" {
		t.Fatalf("explicit hero = %q, %t; want second, true", hero.Slug, ok)
	}
}

func TestResolveNestedHeroesUsesFirstPublishedChild(t *testing.T) {
	parentID := int64(1)
	protectedID := int64(2)
	firstPublishedID := int64(3)
	secondPublishedID := int64(4)
	children := map[int64][]model.Gallery{
		parentID: {
			{ID: protectedID, ParentID: &parentID, Status: model.GalleryProtected},
			{ID: firstPublishedID, ParentID: &parentID, Status: model.GalleryPublished},
			{ID: secondPublishedID, ParentID: &parentID, Status: model.GalleryPublished},
		},
	}
	heroes := map[int64]render.PhotoView{
		protectedID:       {Slug: "private"},
		firstPublishedID:  {Slug: "first-public"},
		secondPublishedID: {Slug: "second-public"},
	}

	(&Builder{}).resolveNestedHeroes(children, heroes)
	if got := heroes[parentID].Slug; got != "first-public" {
		t.Fatalf("nested hero = %q, want first-public", got)
	}
}

func TestProtectedFolderDoesNotInheritPublicCover(t *testing.T) {
	protectedID := int64(1)
	childID := int64(2)
	visible := []model.Gallery{
		{ID: protectedID, Status: model.GalleryProtected},
		{ID: childID, ParentID: &protectedID, Status: model.GalleryPublished},
	}
	covers := map[int64]render.Source{childID: {URL: "/public-child.jpg"}}

	(&Builder{}).resolveNestedCovers(visible, covers)
	if cover := covers[protectedID]; cover.URL != "" {
		t.Fatalf("protected folder inherited public cover %q", cover.URL)
	}
}

func TestHomepageHeroSkipsProtectedRoots(t *testing.T) {
	roots := []model.Gallery{
		{ID: 1, Status: model.GalleryProtected},
		{ID: 2, Status: model.GalleryPublished},
	}
	heroes := map[int64]render.PhotoView{
		1: {Slug: "private"},
		2: {Slug: "public"},
	}
	galleries := map[int64]model.Gallery{
		1: {ID: 1, Status: model.GalleryProtected},
		2: {ID: 2, Status: model.GalleryPublished},
	}

	hero, ok := homepageHero(roots, heroes, galleries, "")
	if !ok || hero.Slug != "public" {
		t.Fatalf("homepage hero = %q, %t; want public, true", hero.Slug, ok)
	}
}

func TestExplicitHeroSourcesOverrideAutomaticHeroes(t *testing.T) {
	sourceID := int64(3)
	galleries := []model.Gallery{
		{ID: 1, Status: model.GalleryPublished, HeroGalleryID: &sourceID},
		{ID: 2, ParentID: int64Pointer(1), Status: model.GalleryPublished},
		{ID: 3, ParentID: int64Pointer(1), Status: model.GalleryPublished},
	}
	byID := make(map[int64]model.Gallery, len(galleries))
	for _, gallery := range galleries {
		byID[gallery.ID] = gallery
	}
	heroes := map[int64]render.PhotoView{
		1: {Slug: "automatic"},
		2: {Slug: "first-child"},
		3: {Slug: "selected"},
	}
	builder := &Builder{byID: byID}

	photos := map[int64][]render.PhotoView{}
	overrides := builder.applyGalleryHeroSources(galleries, photos, heroes)
	if !overrides[1] || heroes[1].Slug != "selected" {
		t.Fatalf("explicit hero = %q, override %t; want selected, true", heroes[1].Slug, overrides[1])
	}
	photos[1] = []render.PhotoView{{Slug: "own-cover"}}
	heroes[1] = render.PhotoView{Slug: "own-cover"}
	overrides = builder.applyGalleryHeroSources(galleries, photos, heroes)
	if overrides[1] || heroes[1].Slug != "own-cover" {
		t.Fatalf("gallery with photos used folder override: hero %q, override %t", heroes[1].Slug, overrides[1])
	}
	hero, ok := homepageHero(galleries[:1], heroes, byID, "3")
	if !ok || hero.Slug != "selected" {
		t.Fatalf("selected homepage hero = %q, %t; want selected, true", hero.Slug, ok)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestRenderStoryPreviewIncludesDraftStoryWithoutPublishing(t *testing.T) {
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
	galleryID, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "draft-story", Title: "Draft Story", Description: "Gallery **introduction**.<script>alert('unsafe')</script>",
		Type: model.GalleryStory, Status: model.GalleryDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateBlock(ctx, model.Block{GalleryID: galleryID, Type: model.BlockHeading, Content: "Arrival"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateBlock(ctx, model.Block{GalleryID: galleryID, Type: model.BlockText, Content: "A **private** draft."}); err != nil {
		t.Fatal(err)
	}
	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	var preview strings.Builder
	if err := New(st, th, cfg).RenderStoryPreview(ctx, galleryID, "/galleries/1/preview", &preview); err != nil {
		t.Fatal(err)
	}
	body := preview.String()
	if !strings.Contains(body, "Draft Story") || !strings.Contains(body, "<strong>introduction</strong>") ||
		!strings.Contains(body, "<h2>Arrival</h2>") || !strings.Contains(body, "<strong>private</strong>") ||
		!strings.Contains(body, `/galleries/1/preview/_curator/assets/theme.css`) {
		t.Fatalf("preview missing draft story content or scoped assets:\n%s", body)
	}
	if strings.Contains(body, "alert('unsafe')") {
		t.Fatal("preview rendered unsafe gallery introduction HTML")
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "draft-story", "index.html")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote public story page: %v", err)
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
		Slug: "trip", Title: "Trip", Description: "A **memorable** trip.", Type: model.GalleryGrid, Status: model.GalleryPublished,
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
	if !strings.Contains(string(page), "<strong>memorable</strong>") || !strings.Contains(string(page), "Visible title") || !strings.Contains(string(page), "Visible description") {
		t.Fatal("inherited title and description defaults were not rendered")
	}
	if strings.Contains(string(page), `class="lb-btn lb-share"`) {
		t.Fatal("photo sharing controls were rendered while the default was off")
	}
	if err := st.SetSetting(ctx, "site.default_gallery_show_sharing", "true"); err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatalf("rebuild with sharing enabled: %v", err)
	}
	page, err = os.ReadFile(galleryPage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `class="lb-btn lb-share"`) || !strings.Contains(string(page), `data-share-src="/_curator/img/`) {
		t.Fatal("enabled sharing controls or share derivative were not rendered")
	}
	if err := st.UpdateGalleryPresentation(ctx, gid, model.VisibilityInherit, model.VisibilityInherit, model.VisibilityInherit, model.VisibilityHide); err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatalf("rebuild with sharing override: %v", err)
	}
	page, err = os.ReadFile(galleryPage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), `class="lb-btn lb-share"`) {
		t.Fatal("gallery sharing override did not hide sharing controls")
	}
	unchanged, err := New(st, th, cfg).BuildReport(ctx)
	if err != nil {
		t.Fatalf("unchanged rebuild: %v", err)
	}
	if !unchanged.Unchanged {
		t.Fatal("unchanged rebuild was not skipped")
	}
	if err := os.Remove(filepath.Join(cfg.OutputDir, "index.html")); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(st, th, cfg).BuildReport(ctx)
	if err != nil {
		t.Fatalf("rebuild missing output: %v", err)
	}
	if recovered.Unchanged {
		t.Fatal("missing output did not invalidate build")
	}
	if err := st.SetSetting(ctx, "site.default_gallery_show_title", "false"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "site.default_gallery_show_description", "false"); err != nil {
		t.Fatal(err)
	}
	changed, err := New(st, th, cfg).BuildReport(ctx)
	if err != nil {
		t.Fatalf("rebuild with hidden metadata: %v", err)
	}
	if changed.Unchanged {
		t.Fatal("settings change did not invalidate build")
	}
	page, err = os.ReadFile(galleryPage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "Visible title") || strings.Contains(string(page), "Visible description") {
		t.Fatal("hidden inherited title or description was rendered")
	}
	if err := st.UpdateGalleryPresentation(ctx, gid, model.VisibilityInherit, model.VisibilityShow, model.VisibilityInherit, model.VisibilityInherit); err != nil {
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
	if err := st.SetFacetEnabled(ctx, "camera", true); err != nil {
		t.Fatal(err)
	}

	gid, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "trip", Title: "Trip", Type: model.GalleryGrid, Status: model.GalleryPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), "trip", "a.jpg"), 1)
	itemID, err := st.CreateItem(ctx, model.Item{
		GalleryID: gid, OriginalPath: filepath.Join("trip", "a.jpg"), Filename: "a.jpg",
		Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: model.ItemPublished,
		Camera: "FUJIFILM GFX 50R", XMPLens: "Voigtlander 15mm",
	})
	if err != nil {
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
	manualPage := filepath.Join(cfg.OutputDir, "browse", "lens", "manual-prime", "index.html")
	canonicalPage := filepath.Join(cfg.OutputDir, "browse", "lens", "nikkor-45mm-f-2-8p-ai-s", "index.html")
	importedCameraPage := filepath.Join(cfg.OutputDir, "browse", "camera", "fujifilm-gfx-50r", "index.html")
	manualCameraPage := filepath.Join(cfg.OutputDir, "browse", "camera", "leica-m6", "index.html")

	build()
	mustNotExist(t, xmpPage)
	mustExist(t, importedCameraPage)

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

	if err := st.UpdateItemPresentation(ctx, itemID, "", "", "", model.ItemPublished, false, "", "stale cached camera", "Manual Prime", "stale cached value"); err != nil {
		t.Fatal(err)
	}
	build()
	mustNotExist(t, mappedPage)
	mustExist(t, manualPage)

	if err := st.SetSetting(ctx, "metadata.lens_name_mappings", "Manual Prime = Nikkor 45mm f/2.8P AI-s"); err != nil {
		t.Fatal(err)
	}
	build()
	mustNotExist(t, manualPage)
	mustExist(t, canonicalPage)

	if err := st.UpdateItemPresentation(ctx, itemID, "", "", "", model.ItemPublished, false, "", "stale cached camera", "", "stale cached value"); err != nil {
		t.Fatal(err)
	}
	build()
	mustNotExist(t, manualPage)
	mustExist(t, mappedPage)

	if err := st.UpdateItemPresentation(ctx, itemID, "", "", "", model.ItemPublished, false, "Leica M6", "stale cached camera", "", ""); err != nil {
		t.Fatal(err)
	}
	build()
	mustNotExist(t, importedCameraPage)
	mustExist(t, manualCameraPage)

	if err := st.UpdateItemPresentation(ctx, itemID, "", "", "", model.ItemPublished, false, "", "stale cached camera", "", ""); err != nil {
		t.Fatal(err)
	}
	build()
	mustNotExist(t, manualCameraPage)
	mustExist(t, importedCameraPage)
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

func TestBuildOrdersChildGalleriesByParentSetting(t *testing.T) {
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

	parentID, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "elsewhere", Title: "Elsewhere", Type: model.GalleryGrid, Status: model.GalleryPublished,
		SortMode: model.SortByFilename, SortDirection: model.SortAscending,
	})
	if err != nil {
		t.Fatal(err)
	}
	zurichID, err := st.CreateGallery(ctx, model.Gallery{
		ParentID: &parentID, Slug: "zurich", Title: "Zurich", Type: model.GalleryGrid, Status: model.GalleryPublished,
		SortMode: model.SortByFilename, SortDirection: model.SortAscending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateGallery(ctx, model.Gallery{
		ParentID: &parentID, Slug: "athens", Title: "Athens", Type: model.GalleryGrid, Status: model.GalleryPublished,
	}); err != nil {
		t.Fatal(err)
	}

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	buildPage := func() string {
		t.Helper()
		if err := New(st, th, cfg).Build(ctx); err != nil {
			t.Fatal(err)
		}
		page, err := os.ReadFile(filepath.Join(cfg.OutputDir, "elsewhere", "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		return string(page)
	}
	assertBefore := func(page, first, second string) {
		t.Helper()
		firstAt, secondAt := strings.Index(page, first), strings.Index(page, second)
		if firstAt < 0 || secondAt < 0 || firstAt >= secondAt {
			t.Fatalf("gallery order does not place %q before %q", first, second)
		}
	}

	assertBefore(buildPage(), "Athens", "Zurich")
	if err := st.SetGalleryItemOrder(ctx, parentID, model.SortByFilename, model.SortDescending); err != nil {
		t.Fatal(err)
	}
	assertBefore(buildPage(), "Zurich", "Athens")

	for _, gallery := range []model.Gallery{
		{ParentID: &zurichID, Slug: "winterthur", Title: "Winterthur", Type: model.GalleryGrid, Status: model.GalleryPublished},
		{ParentID: &zurichID, Slug: "basel", Title: "Basel", Type: model.GalleryGrid, Status: model.GalleryPublished},
	} {
		if _, err := st.CreateGallery(ctx, gallery); err != nil {
			t.Fatal(err)
		}
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatal(err)
	}
	zurichPage, err := os.ReadFile(filepath.Join(cfg.OutputDir, "elsewhere", "zurich", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	assertBefore(string(zurichPage), "Basel", "Winterthur")
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

	darkroom, err := theme.Load(os.DirFS("../../themes/darkroom"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "site.introduction", "Photography by Example Name"); err != nil {
		t.Fatal(err)
	}
	if err := New(st, darkroom, cfg).Build(ctx); err != nil {
		t.Fatal(err)
	}
	folderPage, err := os.ReadFile(filepath.Join(cfg.OutputDir, "2026", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(folderPage), `class="gallery-hero has-image"`) {
		t.Error("empty folder should inherit a hero from its published descendant")
	}
	homepage, err := os.ReadFile(filepath.Join(cfg.OutputDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(homepage), `<h1>Photography by Example Name</h1>`) {
		t.Error("homepage did not render the configured site introduction")
	}

	if err := st.SetSetting(ctx, "theme.darkroom.showHero", "false"); err != nil {
		t.Fatal(err)
	}
	if err := New(st, darkroom, cfg).Build(ctx); err != nil {
		t.Fatal(err)
	}
	folderPage, err = os.ReadFile(filepath.Join(cfg.OutputDir, "2026", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(folderPage), `class="gallery-hero`) ||
		!strings.Contains(string(folderPage), `class="gallery-heading"`) {
		t.Error("disabled Darkroom hero did not render the compact folder heading")
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

func TestDerivativeHashIncludesProcessingVersion(t *testing.T) {
	fileHash := "source-hash"
	preset := "display"
	legacy := sha256.Sum256([]byte(fileHash + ":" + preset))
	legacyHash := hex.EncodeToString(legacy[:])[:16]
	if got := deriveHash(fileHash, preset); got == legacyHash {
		t.Fatalf("deriveHash = %q, still matches the pre-orientation cache key", got)
	}
}

func TestBuildPublishesOnlyVisibleTagsFromPublicPhotos(t *testing.T) {
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
	if err := st.SetSetting(ctx, "metadata.tag_visibility", "hide_selected"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "metadata.tag_selection", "HiddenTagZX"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFacetEnabled(ctx, "tag", true); err != nil {
		t.Fatal(err)
	}

	createTaggedItem := func(slug string, galleryStatus model.GalleryStatus, itemStatus model.ItemStatus, tags ...string) {
		t.Helper()
		galleryID, err := st.CreateGallery(ctx, model.Gallery{Slug: slug, Title: slug, Type: model.GalleryGrid, Status: galleryStatus})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(slug, "photo.jpg")
		if itemStatus == model.ItemPublished {
			writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), path), len(slug))
		}
		itemID, err := st.CreateItem(ctx, model.Item{
			GalleryID: galleryID, OriginalPath: path, Filename: "photo.jpg",
			Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: itemStatus,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.ReplaceItemUserTags(ctx, itemID, tags); err != nil {
			t.Fatal(err)
		}
	}

	createTaggedItem("public-tags", model.GalleryPublished, model.ItemPublished, "VisibleTagZX", "HiddenTagZX")
	createTaggedItem("draft-photo", model.GalleryPublished, model.ItemDraft, "DraftTagZX")
	createTaggedItem("unlisted-tags", model.GalleryUnlisted, model.ItemPublished, "UnlistedTagZX")
	createTaggedItem("protected-tags", model.GalleryProtected, model.ItemPublished, "ProtectedTagZX")

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	if err := New(st, th, cfg).Build(ctx); err != nil {
		t.Fatalf("build: %v", err)
	}

	publicPage, err := os.ReadFile(filepath.Join(cfg.OutputDir, "public-tags", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicPage), `class="fig-tags"`) {
		t.Fatalf("grid gallery rendered a visible tag label:\n%s", publicPage)
	}
	if !strings.Contains(string(publicPage), `class="lightbox-tags-source" hidden`) || !strings.Contains(string(publicPage), "visibletagzx") {
		t.Fatalf("grid gallery does not contain the permitted lightbox tag source:\n%s", publicPage)
	}

	tagIndex, err := os.ReadFile(filepath.Join(cfg.OutputDir, "browse", "tag", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tagIndex), "visibletagzx") {
		t.Fatalf("tag index does not contain visible tag:\n%s", tagIndex)
	}
	allPublicHTML := string(publicPage) + string(tagIndex)
	for _, hidden := range []string{"hiddentagzx", "drafttagzx", "unlistedtagzx", "protectedtagzx"} {
		if strings.Contains(allPublicHTML, hidden) {
			t.Errorf("public tag output leaked %q", hidden)
		}
		if _, err := os.Stat(filepath.Join(cfg.OutputDir, "browse", "tag", strings.ToLower(hidden), "index.html")); !os.IsNotExist(err) {
			t.Errorf("browse page for %q exists or returned unexpected error: %v", hidden, err)
		}
	}
	for _, gallery := range []string{"unlisted-tags", "protected-tags"} {
		page, err := os.ReadFile(filepath.Join(cfg.OutputDir, gallery, "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(page), "tagzx") {
			t.Errorf("%s gallery leaked user tags", gallery)
		}
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
	if err := st.SetSetting(ctx, "site.default_gallery_show_sharing", "true"); err != nil {
		t.Fatal(err)
	}

	gid, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "secret", Title: "Secret", Type: model.GalleryGrid, Status: model.GalleryProtected, ShowSharing: model.VisibilityShow,
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
	protectedPage, err := os.ReadFile(filepath.Join(cfg.OutputDir, "secret", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(protectedPage), `class="lb-btn lb-share"`) {
		t.Fatal("protected gallery rendered sharing controls")
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
