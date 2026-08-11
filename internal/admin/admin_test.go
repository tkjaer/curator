package admin

import (
	"bytes"
	"context"
	"errors"
	"html"
	"image"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tkjaer/curator/internal/build"
	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/publishapi"
	"github.com/tkjaer/curator/internal/store"
)

func newTestServer(t *testing.T) (*Server, chan struct{}) {
	t.Helper()
	tmp := t.TempDir()
	cfg := config.New(tmp, filepath.Join(tmp, "output"))

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	built := make(chan struct{}, 1)
	srv, err := New(st, cfg, Options{Build: func(context.Context, func(build.Progress)) (build.Report, error) {
		select {
		case built <- struct{}{}:
		default:
		}
		return build.Report{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return srv, built
}

func TestDashboardRenders(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Galleries") {
		t.Error("dashboard missing expected content")
	}
}

func TestTagReviewLinksToPhotoTagEditor(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "published", Title: "Published gallery", Status: model.GalleryPublished})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := srv.store.CreateItem(ctx, model.Item{
		GalleryID: galleryID, OriginalPath: "published/photo.jpg", Filename: "photo.jpg", Status: model.ItemPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ReplaceItemUserTags(ctx, itemID, []string{"shared", "hidden"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ReplaceItemImportedTags(ctx, itemID, store.TagSourceMetadata, []string{"shared"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ReplaceItemImportedTags(ctx, itemID, store.TagSourceLightroom, []string{"shared"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.ExecContext(ctx, `UPDATE facet_config SET enabled = 1 WHERE namespace = 'tag'`); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetSetting(ctx, "metadata.tag_visibility", "hide_selected"); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetSetting(ctx, "metadata.tag_selection", "hidden"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tags", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `data-name="shared" data-count="1" data-public="true"`) ||
		!strings.Contains(body, `Curator, Metadata, Lightroom`) ||
		!strings.Contains(body, `data-name="hidden" data-count="1" data-public="false"`) {
		t.Fatalf("tag review status = %d, body = %s", rec.Code, body)
	}

	var tagID int64
	if err := srv.store.DB.QueryRowContext(ctx, `SELECT id FROM tags WHERE namespace = 'user' AND value = 'shared'`).Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tags/"+strconv.FormatInt(tagID, 10), nil))
	body = rec.Body.String()
	wantLink := `/galleries/` + strconv.FormatInt(galleryID, 10) + `?photo=` + strconv.FormatInt(itemID, 10) + `&amp;tab=tags&amp;return_tag=` + strconv.FormatInt(tagID, 10)
	if rec.Code != http.StatusOK || !strings.Contains(body, `<h1>shared</h1>`) ||
		!strings.Contains(body, `src="/media/published/photo.jpg"`) || !strings.Contains(body, wantLink) ||
		!strings.Contains(body, `<h2 class="tag-gallery-heading">Published gallery <span>1 photo</span></h2>`) ||
		!strings.Contains(body, `<span class="tag-photo-source">Lightroom</span>`) ||
		strings.Contains(body, `<span class="tag-photo-source">Curator`) {
		t.Fatalf("tag detail status = %d, body = %s", rec.Code, body)
	}

	if err := srv.store.ReplaceItemImportedTags(ctx, itemID, store.TagSourceLightroom, nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ReplaceItemEditableTags(ctx, itemID, []string{"hidden"}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tags/"+strconv.FormatInt(tagID, 10), nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/tags" {
		t.Fatalf("removed tag redirect = %d %q, want 303 /tags", rec.Code, rec.Header().Get("Location"))
	}
}

func TestDashboardRendersCollapsibleGalleryTree(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	parentID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "2026", Title: "2026"})
	if err != nil {
		t.Fatal(err)
	}
	childID, err := srv.store.CreateGallery(ctx, model.Gallery{ParentID: &parentID, Slug: "summer", Title: "Summer"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []model.Item{
		{GalleryID: parentID, OriginalPath: "2026/cover.jpg", Filename: "cover.jpg"},
		{GalleryID: childID, OriginalPath: "2026/summer/first.jpg", Filename: "first.jpg"},
		{GalleryID: childID, OriginalPath: "2026/summer/second.jpg", Filename: "second.jpg"},
	} {
		if _, err := srv.store.CreateItem(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="gallery-tree"`,
		`data-gallery-id="1" data-parent-id="" data-depth="0"`,
		`aria-expanded="false" aria-label="Expand 2026"`,
		`data-gallery-id="2" data-parent-id="1" data-depth="1"`,
		`<th class="gallery-tree-count">Photos</th><th class="gallery-tree-count">Incl. subfolders</th>`,
		`<td class="gallery-tree-count">1</td>`,
		`<td class="gallery-tree-count">3</td>`,
		`<td class="gallery-tree-count"><span class="muted">&mdash;</span></td>`,
		`curator-expanded-galleries:`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestDashboardCanReorderGalleries(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	var galleryIDs []int64
	for _, title := range []string{"First", "Second", "Third"} {
		galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: strings.ToLower(title), Title: title})
		if err != nil {
			t.Fatal(err)
		}
		galleryIDs = append(galleryIDs, galleryID)
	}

	form := url.Values{"direction": {"earlier"}}
	req := httptest.NewRequest(http.MethodPost, "/galleries/3/position", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?msg=Gallery+order+updated" {
		t.Fatalf("position response = %d, location %q", rec.Code, rec.Header().Get("Location"))
	}

	galleries, err := srv.store.Galleries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := []int64{galleries[0].ID, galleries[1].ID, galleries[2].ID}
	want := []int64{galleryIDs[0], galleryIDs[2], galleryIDs[1]}
	if !slices.Equal(got, want) {
		t.Fatalf("gallery order = %v, want %v", got, want)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`aria-label="Move First earlier" title="Move earlier" disabled`,
		`aria-label="Move Third earlier" title="Move earlier" >`,
		`aria-label="Move Second later" title="Move later" disabled`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestGalleryRendersHierarchyAndSecondarySettings(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	parentID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "2026", Title: "2026"})
	if err != nil {
		t.Fatal(err)
	}
	childID, err := srv.store.CreateGallery(ctx, model.Gallery{ParentID: &parentID, Slug: "summer", Title: "Summer"})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/galleries/"+strconv.FormatInt(childID, 10), nil))
	body := rec.Body.String()
	for _, want := range []string{
		`<nav class="breadcrumbs" aria-label="Gallery hierarchy">`,
		`<a href="/galleries/1">2026</a>`,
		`<span aria-current="page">Summer</span>`,
		`<div class="settings-links public-url-row">`,
		`Open public gallery &nearr;`,
		`<details class="gallery-options">`,
		`<span class="disclosure-title">Gallery options</span>`,
		`<input id="gallery-url-name" type="text" name="slug" value="summer" aria-describedby="url-name-format url-name-warning" required>`,
		`<button type="submit" class="secondary-button">Update URL</button>`,
		`<strong>Allowed:</strong> a-z, 0-9, and hyphens.`,
		`<strong>Changing this breaks existing and descendant links.</strong> No redirects are kept.`,
		`<section class="gallery-option-section gallery-danger">`,
		`Delete this gallery and everything inside it. This cannot be undone.`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("gallery missing %q", want)
		}
	}
	if got := strings.Count(body, `<form class="upload-form"`); got != 1 {
		t.Errorf("empty gallery upload forms = %d, want 1", got)
	}
}

func TestGalleryRendersStoryGuidance(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	storyID, err := srv.store.CreateGallery(ctx, model.Gallery{
		Slug: "essay", Title: "Essay", Type: model.GalleryStory,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, want := range []string{
		`<option value="grid">Grid - photo collection</option>`,
		`<option value="story">Story - sequenced essay</option>`,
		`A grid publishes its photo order. A story publishes an authored sequence`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("dashboard missing %q", want)
		}
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/galleries/"+strconv.FormatInt(storyID, 10), nil))
	for _, want := range []string{
		`<ol class="story-workflow" aria-label="Story workflow">`,
		`The published page follows the block order below, not the order of the media library.`,
		`Photos remain unpublished here until an image or grid block uses them.`,
		`Heading - section title`,
		`Grid - group of photos`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("story gallery missing %q", want)
		}
	}

	if _, err := srv.store.CreateBlock(ctx, model.Block{GalleryID: storyID, Type: model.BlockHeading, Content: "Arrival"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CreateBlock(ctx, model.Block{GalleryID: storyID, Type: model.BlockQuote, Content: "A remembered line"}); err != nil {
		t.Fatal(err)
	}
	srv.storyPreview = func(_ context.Context, galleryID int64, baseURL string, w io.Writer) error {
		if galleryID != storyID || baseURL != "/galleries/"+strconv.FormatInt(storyID, 10)+"/preview" {
			t.Fatalf("preview request = gallery %d, base %q", galleryID, baseURL)
		}
		_, err := io.WriteString(w, "<h1>Draft preview</h1>")
		return err
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/galleries/"+strconv.FormatInt(storyID, 10), nil))
	for _, want := range []string{
		`class="story-block block-heading"`,
		`<strong>Heading</strong><span>Section title</span>`,
		`class="save-state block-save-state"`,
		`<input type="text" name="content" value="Arrival" placeholder="Section title">`,
		`<span>Quotation (Markdown)</span>`,
		`<strong>Add the next block</strong>`,
		`<select name="type" id="add-block-type">`,
		`<span>Heading (optional)</span>`,
		`<button>Add to story</button>`,
		`>Preview story &nearr;</a>`,
		`if (!form || event.defaultPrevented) return;`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("populated story gallery missing %q", want)
		}
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/galleries/"+strconv.FormatInt(storyID, 10)+"/preview", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), "Draft preview") {
		t.Fatalf("story preview response = %d, cache %q, body %q", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
}

func TestGalleryRendersCompactPhotoEditor(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "photos", Title: "Photos"})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := srv.store.CreateItem(ctx, model.Item{
		GalleryID: galleryID, OriginalPath: "photos/image.jpg", Filename: "image.jpg",
		Title: `A "title" <unsafe>`, Description: "<b>Description</b>", Camera: `A "manual" <camera>`, EmbeddedCamera: "Frontier", ManualCamera: `A "manual" <camera>`, Lens: `A "manual" <lens>`, ManualLens: `A "manual" <lens>`, Status: model.ItemPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ReplaceItemUserTags(ctx, itemID, []string{`A "tag" <unsafe>`}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/galleries/"+strconv.FormatInt(galleryID, 10), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="item-preview"`,
		`data-item-id="` + strconv.FormatInt(itemID, 10) + `"`,
		`data-title="A &#34;title&#34; &lt;unsafe&gt;"`,
		`data-description="&lt;b&gt;Description&lt;/b&gt;"`,
		`data-tags="a &#34;tag&#34; &lt;unsafe&gt;"`,
		`data-embedded-camera="Frontier"`,
		`data-manual-camera="A &#34;manual&#34; &lt;camera&gt;"`,
		`data-manual-lens="A &#34;manual&#34; &lt;lens&gt;"`,
		`data-update-action="/items/` + strconv.FormatInt(itemID, 10) + `/update"`,
		`<dialog class="photo-editor-dialog" id="photo-editor">`,
		`role="tablist" aria-label="Photo editor sections"`,
		`data-photo-editor-panel="metadata" hidden`,
		`name="manual_camera" list="camera-suggestions"`,
		`<datalist id="camera-suggestions">`,
		`<option value="A &#34;manual&#34; &lt;camera&gt;" label="1 photo"></option>`,
		`name="manual_lens" list="lens-suggestions"`,
		`<datalist id="lens-suggestions">`,
		`<option value="A &#34;manual&#34; &lt;lens&gt;" label="1 photo"></option>`,
		`data-photo-editor-panel="tags" hidden`,
		`name="tags" list="tag-suggestions"`,
		`<option value="a &#34;tag&#34; &lt;unsafe&gt;"></option>`,
		`.cover-label[hidden] { display: none; }`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("gallery photo editor missing %q", want)
		}
	}
	if strings.Count(body, `<dialog class="photo-editor-dialog"`) != 1 {
		t.Error("gallery should render one shared photo editor dialog")
	}
	uploadAction := `action="/galleries/` + strconv.FormatInt(galleryID, 10) + `/upload"`
	if got := strings.Count(body, `<form class="upload-form"`); got != 2 {
		t.Errorf("gallery upload forms = %d, want 2", got)
	}
	if got := strings.Count(body, uploadAction); got != 2 {
		t.Errorf("gallery upload actions = %d, want 2", got)
	}
}

func TestItemCoverCanBeSetAndRemoved(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "photos", Title: "Photos"})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := srv.store.CreateItem(ctx, model.Item{
		GalleryID: galleryID, OriginalPath: "photos/image.jpg", Filename: "image.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}

	postCover := func() {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/items/"+strconv.FormatInt(itemID, 10)+"/cover", nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
		}
	}

	postCover()
	g, err := srv.store.Gallery(ctx, galleryID)
	if err != nil {
		t.Fatal(err)
	}
	if g.CoverItemID == nil || *g.CoverItemID != itemID {
		t.Fatalf("cover = %v, want %d", g.CoverItemID, itemID)
	}

	postCover()
	g, err = srv.store.Gallery(ctx, galleryID)
	if err != nil {
		t.Fatal(err)
	}
	if g.CoverItemID != nil {
		t.Fatalf("cover = %d, want nil", *g.CoverItemID)
	}
}

func TestItemUpdateSetsAndClearsManualMetadata(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "photos", Title: "Photos"})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := srv.store.CreateItem(ctx, model.Item{
		GalleryID: galleryID, OriginalPath: "photos/image.jpg", Filename: "image.jpg",
		Status: model.ItemPublished, EmbeddedCamera: "Frontier", Camera: "Frontier", EmbeddedLens: "Detected lens", Lens: "Detected lens",
	})
	if err != nil {
		t.Fatal(err)
	}

	update := func(manualCamera, manualLens, tags string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"status": {string(model.ItemPublished)}, "manual_camera": {manualCamera}, "manual_lens": {manualLens}, "tags": {tags}}
		req := httptest.NewRequest(http.MethodPost, "/items/"+strconv.FormatInt(itemID, 10)+"/update", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Curator-Async", "true")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	rec := update("  Leica M6  ", "  Manual lens  ", " night; Night, Kodak   Portra 400 ")
	if body := rec.Body.String(); !strings.Contains(body, `"resolvedCamera":"Leica M6"`) {
		t.Fatalf("manual update response = %q", body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"resolvedLens":"Manual lens"`) {
		t.Fatalf("manual update response = %q", body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"tags":"kodak portra 400, night"`) {
		t.Fatalf("tag update response = %q", body)
	}
	tags, err := srv.store.ItemUserTags(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0].Value != "kodak portra 400" || tags[1].Value != "night" {
		t.Fatalf("stored tags = %#v", tags)
	}
	it, err := srv.store.Item(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if it.EmbeddedCamera != "Frontier" || it.ManualCamera != "Leica M6" || it.Camera != "Leica M6" || it.ManualLens != "Manual lens" || it.Lens != "Manual lens" {
		t.Fatalf("manual update stored %+v", it)
	}

	rec = update("", "", "")
	if body := rec.Body.String(); !strings.Contains(body, `"resolvedCamera":"Frontier"`) {
		t.Fatalf("clear response = %q", body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"resolvedLens":"Detected lens"`) {
		t.Fatalf("clear response = %q", body)
	}
	tags, err = srv.store.ItemUserTags(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Fatalf("tags after clear = %#v, want none", tags)
	}
	it, err = srv.store.Item(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if it.EmbeddedCamera != "Frontier" || it.ManualCamera != "" || it.Camera != "Frontier" || it.ManualLens != "" || it.Lens != "Detected lens" {
		t.Fatalf("cleared manual update stored %+v", it)
	}
}

func TestItemUpdateEditsUploadedTagsButNotLightroomTags(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "photos", Title: "Photos"})
	if err != nil {
		t.Fatal(err)
	}
	createItem := func(filename string) int64 {
		t.Helper()
		itemID, err := srv.store.CreateItem(ctx, model.Item{
			GalleryID: galleryID, OriginalPath: "photos/" + filename, Filename: filename, Status: model.ItemPublished,
		})
		if err != nil {
			t.Fatal(err)
		}
		return itemID
	}
	update := func(itemID int64, tags string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"status": {string(model.ItemPublished)}, "tags": {tags}}
		req := httptest.NewRequest(http.MethodPost, "/items/"+strconv.FormatInt(itemID, 10)+"/update", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Curator-Async", "true")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	uploadedID := createItem("uploaded.jpg")
	if err := srv.store.ReplaceItemImportedTags(ctx, uploadedID, store.TagSourceMetadata, []string{"keep", "remove"}); err != nil {
		t.Fatal(err)
	}
	if body := update(uploadedID, "keep; added").Body.String(); !strings.Contains(body, `"tags":"added, keep"`) {
		t.Fatalf("uploaded update response = %q", body)
	}
	if err := srv.store.ReplaceItemImportedTags(ctx, uploadedID, store.TagSourceMetadata, []string{"keep", "remove", "new"}); err != nil {
		t.Fatal(err)
	}
	assertAdminTagValues(t, srv, uploadedID, []string{"added", "keep", "new"})

	lightroomID := createItem("lightroom.jpg")
	if err := srv.store.SetExternalItem(ctx, "lightroom", "photo-1", lightroomID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ReplaceItemImportedTags(ctx, lightroomID, store.TagSourceLightroom, []string{"from lightroom"}); err != nil {
		t.Fatal(err)
	}
	if body := update(lightroomID, "curator").Body.String(); !strings.Contains(body, `"tags":"curator"`) {
		t.Fatalf("Lightroom update response = %q", body)
	}
	assertAdminTagValues(t, srv, lightroomID, []string{"curator", "from lightroom"})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/galleries/"+strconv.FormatInt(galleryID, 10), nil))
	body := rec.Body.String()
	for _, want := range []string{
		`data-item-id="` + strconv.FormatInt(uploadedID, 10) + `" data-filename="uploaded.jpg" data-title="" data-description="" data-caption="" data-tags="added, keep, new" data-imported-tags="" data-lightroom-managed="false"`,
		`data-item-id="` + strconv.FormatInt(lightroomID, 10) + `" data-filename="lightroom.jpg" data-title="" data-description="" data-caption="" data-tags="curator" data-imported-tags="from lightroom" data-lightroom-managed="true"`,
		`<strong>From Lightroom:</strong>`,
		`Edit keywords in Lightroom and republish.`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("gallery tag editor missing %q", want)
		}
	}
}

func assertAdminTagValues(t *testing.T, srv *Server, itemID int64, want []string) {
	t.Helper()
	tags, err := srv.store.ItemUserTags(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(tags))
	for index, tag := range tags {
		got[index] = tag.Value
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
}

func TestGalleryDefaultsCanBeSavedAndApplied(t *testing.T) {
	srv, _ := newTestServer(t)
	handler := srv.Handler()

	settings := url.Values{
		"default_gallery_order":            {"date"},
		"default_gallery_published":        {"on"},
		"default_gallery_show_exif":        {"on"},
		"default_gallery_show_title":       {"on"},
		"default_gallery_show_description": {"on"},
	}
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(settings.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save settings status = %d", rec.Code)
	}

	form := url.Values{"title": {"Published by default"}}
	req = httptest.NewRequest(http.MethodPost, "/galleries", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create gallery status = %d", rec.Code)
	}
	galleries, err := srv.store.Galleries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(galleries) != 1 || galleries[0].Status != model.GalleryPublished || galleries[0].PublishedAt == nil {
		t.Fatalf("gallery defaults not applied: %#v", galleries)
	}
	g := galleries[0]
	if g.ShowEXIF != model.VisibilityInherit || g.ShowTitle != model.VisibilityInherit || g.ShowDescription != model.VisibilityInherit {
		t.Fatalf("new gallery presentation should inherit: %#v", g)
	}
	defaults, err := srv.store.GalleryPresentationDefaults(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !g.ShowEXIF.Resolve(defaults.ShowEXIF) || !g.ShowTitle.Resolve(defaults.ShowTitle) || !g.ShowDescription.Resolve(defaults.ShowDescription) {
		t.Fatalf("gallery presentation defaults not resolved: %#v", defaults)
	}
}

func TestGalleryPresentationOverridesCanBeReset(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	id, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "gallery", Title: "Gallery", Type: model.GalleryGrid})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateGalleryPresentation(ctx, id, model.VisibilityShow, model.VisibilityHide, model.VisibilityShow); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"field": {"title"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/gallery-presentation/reset", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("selective reset status = %d", rec.Code)
	}
	gallery, err := srv.store.Gallery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if gallery.ShowTitle != model.VisibilityInherit || gallery.ShowEXIF != model.VisibilityShow || gallery.ShowDescription != model.VisibilityShow {
		t.Fatalf("selective gallery presentation reset changed other fields: %#v", gallery)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/settings/gallery-presentation/reset", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reset-all status = %d", rec.Code)
	}
	gallery, err = srv.store.Gallery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if gallery.ShowEXIF != model.VisibilityInherit || gallery.ShowTitle != model.VisibilityInherit || gallery.ShowDescription != model.VisibilityInherit {
		t.Fatalf("gallery presentation not reset: %#v", gallery)
	}
}

func TestPublishingTokenCanBeCreatedInUI(t *testing.T) {
	srv, _ := newTestServer(t)
	handler := srv.Handler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/settings/publishing/token", nil))

	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create token status = %d, cache = %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	match := regexp.MustCompile(`id="publishing-token" value="([A-Za-z0-9_-]+)"`).FindStringSubmatch(rec.Body.String())
	if match == nil {
		t.Fatalf("generated token not shown in response")
	}
	token := match[1]
	settings, err := srv.store.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings["publish.api_token_hash"] == token || settings["publish.api_token_hash"] != publishapi.TokenHash(token) {
		t.Fatalf("publishing token was not stored as its hash")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("new token status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/publishing", nil))
	if strings.Contains(rec.Body.String(), token) || !strings.Contains(rec.Body.String(), "Rotate publishing token") {
		t.Fatalf("token was redisplayed or configured state was missing")
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/settings/publishing/token", nil))
	rotated := regexp.MustCompile(`id="publishing-token" value="([A-Za-z0-9_-]+)"`).FindStringSubmatch(rec.Body.String())
	if rotated == nil || rotated[1] == token {
		t.Fatalf("rotated token was not generated")
	}
	for candidate, wantStatus := range map[string]int{token: http.StatusUnauthorized, rotated[1]: http.StatusOK} {
		req = httptest.NewRequest(http.MethodGet, "/api/v1/", nil)
		req.Header.Set("Authorization", "Bearer "+candidate)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("token status = %d, want %d", rec.Code, wantStatus)
		}
	}
}

func TestRsyncDeploymentCanBeConfiguredInUI(t *testing.T) {
	srv, _ := newTestServer(t)
	form := url.Values{
		"rsync_enabled": {"on"},
		"rsync_target":  {"photos@example.com:/srv/site"},
		"rsync_delete":  {"on"},
	}
	req := httptest.NewRequest(http.MethodPost, "/settings/publishing", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save publishing settings status = %d", rec.Code)
	}

	settings, err := srv.store.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"publish.rsync_enabled": "true",
		"publish.rsync_target":  "photos@example.com:/srv/site",
		"publish.rsync_delete":  "true",
	} {
		if settings[key] != want {
			t.Errorf("%s = %q, want %q", key, settings[key], want)
		}
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/publishing", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `name="rsync_enabled" checked`) ||
		!strings.Contains(body, `value="photos@example.com:/srv/site"`) ||
		!strings.Contains(body, `name="rsync_delete" checked`) {
		t.Fatalf("saved rsync settings not rendered: %s", body)
	}
}

func TestRsyncCleanupCanBePreviewedBeforeEnablingDeployment(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := srv.store.SetSetting(context.Background(), "publish.rsync_target", "photos@example.com:/srv/site"); err != nil {
		t.Fatal(err)
	}
	called := false
	srv.previewDeploy = func(_ context.Context, target string, delete bool) (string, error) {
		called = true
		if target != "photos@example.com:/srv/site" || !delete {
			t.Fatalf("preview = %q, delete %t", target, delete)
		}
		return "*deleting old <photo>.jpg\n>f+++++++++ new.jpg", nil
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/settings/publishing/preview", nil))
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("preview response = %d, called %t", rec.Code, called)
	}
	body := rec.Body.String()
	decoded := html.UnescapeString(body)
	if strings.Contains(body, "old <photo>.jpg") || !strings.Contains(decoded, "*deleting old <photo>.jpg") ||
		!strings.Contains(decoded, ">f+++++++++ new.jpg") {
		t.Fatalf("preview output not safely rendered: %s", body)
	}
	deployment := strings.Index(body, `id="deployment-settings"`)
	preview := strings.Index(body, `class="deployment-preview"`)
	feed := strings.Index(body, `<h2>Atom feed</h2>`)
	if deployment < 0 || preview < deployment || feed < preview || !strings.Contains(body, `form="rsync-preview-form"`) || !strings.Contains(body, `id="rsync-preview-form"`) {
		t.Fatalf("preview is not contained by the deployment panel: %s", body)
	}
}

func TestBuildDeploysConfiguredRsyncTarget(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	for key, value := range map[string]string{
		"publish.rsync_enabled": "true",
		"publish.rsync_target":  "photos@example.com:/srv/site",
		"publish.rsync_delete":  "true",
	} {
		if err := srv.store.SetSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}

	var steps []string
	srv.build = func(context.Context, func(build.Progress)) (build.Report, error) {
		steps = append(steps, "build")
		return build.Report{Galleries: 2, Photos: 12}, nil
	}
	srv.deploy = func(_ context.Context, target string, delete bool) error {
		steps = append(steps, "deploy")
		if target != "photos@example.com:/srv/site" || !delete {
			t.Fatalf("deployment = %q, delete %t", target, delete)
		}
		return nil
	}
	if !srv.builds.begin() {
		t.Fatal("build did not begin")
	}
	srv.runBuildQueue()

	if strings.Join(steps, ",") != "build,deploy" {
		t.Fatalf("publish steps = %v", steps)
	}
	status := srv.builds.snapshot()
	if status.Running || status.Error != "" || status.Stage != "Rsync" ||
		status.RsyncStatus != "complete" || status.RsyncTarget != "photos@example.com:/srv/site" {
		t.Fatalf("publish status = %#v", status)
	}
	settings, err := srv.store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, settings["publish.last_success_at"]); err != nil {
		t.Fatalf("last successful publish = %q: %v", settings["publish.last_success_at"], err)
	}
}

func TestFailedBuildDoesNotDeploy(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := srv.store.SetSetting(context.Background(), "publish.rsync_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	srv.build = func(context.Context, func(build.Progress)) (build.Report, error) {
		return build.Report{}, errors.New("render failed")
	}
	deployed := false
	srv.deploy = func(context.Context, string, bool) error {
		deployed = true
		return nil
	}
	if !srv.builds.begin() {
		t.Fatal("build did not begin")
	}
	srv.runBuildQueue()

	if deployed {
		t.Fatal("deployment ran after a failed build")
	}
	if got := srv.builds.snapshot().Error; got != "render failed" {
		t.Fatalf("publish error = %q", got)
	}
}

func TestFailedRsyncIsReportedSeparately(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	for key, value := range map[string]string{
		"publish.rsync_enabled":   "true",
		"publish.rsync_target":    "photos@example.com:/srv/site",
		"publish.last_success_at": "2026-08-06T12:00:00Z",
	} {
		if err := srv.store.SetSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	srv.build = func(context.Context, func(build.Progress)) (build.Report, error) {
		return build.Report{Galleries: 1}, nil
	}
	srv.deploy = func(context.Context, string, bool) error {
		return errors.New("connection refused")
	}
	if !srv.builds.begin() {
		t.Fatal("build did not begin")
	}
	srv.runBuildQueue()

	status := srv.builds.snapshot()
	if status.Running || status.RsyncStatus != "failed" || status.RsyncTarget != "photos@example.com:/srv/site" ||
		status.Error != "connection refused" {
		t.Fatalf("publish status = %#v", status)
	}
	settings, err := srv.store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := settings["publish.last_success_at"]; got != "2026-08-06T12:00:00Z" {
		t.Fatalf("last successful publish = %q after failed rsync", got)
	}
}

func TestBuildStatusReportsPersistedLastPublish(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := srv.store.SetSetting(context.Background(), "publish.last_success_at", "2026-08-06T12:00:00Z"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/build/status", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"lastPublished":"2026-08-06T12:00:00Z"`) {
		t.Fatalf("build status = %d, %s", rec.Code, rec.Body.String())
	}
}

func TestBuildStatusIDsDistinguishBuilds(t *testing.T) {
	status := newBuildStatus()
	if !status.begin() {
		t.Fatal("first build did not begin")
	}
	first := status.snapshot().BuildID
	status.finish(build.Report{FeedUpdated: true, Unchanged: true}, nil)
	if snapshot := status.snapshot(); !snapshot.FeedUpdated || !snapshot.Unchanged {
		t.Fatal("build report fields missing from build status")
	}
	if !status.begin() {
		t.Fatal("second build did not begin")
	}
	second := status.snapshot().BuildID
	if first == 0 || second != first+1 {
		t.Fatalf("build IDs = %d, %d", first, second)
	}
}

func TestBuildStatusCoalescesPendingBuilds(t *testing.T) {
	status := newBuildStatus()
	if !status.begin() {
		t.Fatal("first build did not begin")
	}
	if status.queue() {
		t.Fatal("pending build started while another build was running")
	}
	if status.queue() {
		t.Fatal("pending build started while another build was running")
	}
	if !status.finish(build.Report{}, nil) {
		t.Fatal("pending build was not scheduled after the running build")
	}
	if snapshot := status.snapshot(); !snapshot.Running || snapshot.BuildID != 2 {
		t.Fatalf("queued build status = %#v", snapshot)
	}
	if status.finish(build.Report{}, nil) {
		t.Fatal("unexpected third build")
	}
}

func TestBuildStatusInstancesDistinguishRestarts(t *testing.T) {
	first := newBuildStatus()
	second := newBuildStatus()
	if !first.begin() || !second.begin() {
		t.Fatal("build did not begin")
	}
	firstStatus := first.snapshot()
	secondStatus := second.snapshot()
	if firstStatus.BuildID != secondStatus.BuildID || firstStatus.BuildInstance == secondStatus.BuildInstance {
		t.Fatalf("build identities = %q:%d and %q:%d",
			firstStatus.BuildInstance, firstStatus.BuildID, secondStatus.BuildInstance, secondStatus.BuildID)
	}
}

func TestUploadStemPairsSidecars(t *testing.T) {
	for _, name := range []string{"photo.jpg", "photo.xmp", "photo.jpg.xmp", "PHOTO.XMP"} {
		if got := uploadStem(name); got != "photo" {
			t.Errorf("uploadStem(%q) = %q, want photo", name, got)
		}
	}
}

func TestUploadRejectsDuplicateFilenameInGallery(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "manual", Title: "Manual"})
	if err != nil {
		t.Fatal(err)
	}

	upload := func(filename string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("images", filename)
		if err != nil {
			t.Fatal(err)
		}
		if err := jpeg.Encode(part, image.NewRGBA(image.Rect(0, 0, 4, 3)), nil); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/galleries/"+strconv.FormatInt(galleryID, 10)+"/upload", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	if rec := upload("photo.jpg"); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "1+image+uploaded") {
		t.Fatalf("first upload = %d, %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := upload("PHOTO.JPG"); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "PHOTO.JPG+is+already+in+this+gallery") {
		t.Fatalf("duplicate upload = %d, %q", rec.Code, rec.Header().Get("Location"))
	}
	items, err := srv.store.ItemsByGallery(ctx, galleryID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
}

func TestDeleteKeepsOriginalReferencedByLegacyDuplicate(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "manual", Title: "Manual"})
	if err != nil {
		t.Fatal(err)
	}
	item := model.Item{GalleryID: galleryID, OriginalPath: "manual/photo.jpg", Filename: "photo.jpg"}
	firstID, err := srv.store.CreateItem(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := srv.store.CreateItem(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(srv.cfg.OriginalsDir(), "manual", "photo.jpg")
	if err := os.MkdirAll(filepath.Dir(originalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalPath, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}

	deleteItem := func(itemID int64) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/items/"+strconv.FormatInt(itemID, 10)+"/delete", nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("delete status = %d, want 303", rec.Code)
		}
	}

	deleteItem(firstID)
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("shared original removed: %v", err)
	}
	deleteItem(secondID)
	if _, err := os.Stat(originalPath); !os.IsNotExist(err) {
		t.Fatalf("unreferenced original still exists: %v", err)
	}
}

func TestCreateGalleryThenAppears(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	form := url.Values{"title": {"Spring Trip"}}
	req := httptest.NewRequest("POST", "/galleries", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/galleries/1") {
		t.Errorf("redirect = %q, want /galleries/1...", loc)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), "Spring Trip") {
		t.Error("new gallery not listed on dashboard")
	}
}

func TestAsyncMutationReturnsJSONInsteadOfRedirect(t *testing.T) {
	srv, _ := newTestServer(t)
	form := url.Values{"title": {"Async Gallery"}}
	req := httptest.NewRequest("POST", "/galleries", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Curator-Async", "true")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("async status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("async response redirected to %q", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"message":"Gallery created"`) {
		t.Errorf("async response = %q, want JSON message", body)
	}
}

func TestLocalGalleryTitleCanChangeWithoutChangingSlug(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	id, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "original-url", Title: "Original title"})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"title": {"New title"}}
	req := httptest.NewRequest("POST", "/galleries/1/title", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Curator-Async", "true")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Title updated") {
		t.Fatalf("title response = %d %q", rec.Code, rec.Body.String())
	}
	g, err := srv.store.Gallery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if g.Title != "New title" || g.Slug != "original-url" {
		t.Fatalf("updated gallery = %#v", g)
	}
}

func TestLightroomGalleryTitleIsManagedButSlugCanChange(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	id, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "lightroom-title", Title: "Lightroom title"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetExternalGallery(ctx, "lightroom", "collection-1", id); err != nil {
		t.Fatal(err)
	}

	h := srv.Handler()
	form := url.Values{"title": {"Curator title"}}
	req := httptest.NewRequest("POST", "/galleries/1/title", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Curator-Async", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "managed by Lightroom") {
		t.Fatalf("managed title response = %d %q", rec.Code, rec.Body.String())
	}
	g, err := srv.store.Gallery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if g.Title != "Lightroom title" {
		t.Fatalf("managed title = %q", g.Title)
	}

	form = url.Values{"slug": {"New Website URL"}}
	req = httptest.NewRequest("POST", "/galleries/1/slug", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Curator-Async", "true")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Gallery URL changed") {
		t.Fatalf("slug response = %d %q", rec.Code, rec.Body.String())
	}
	g, err = srv.store.Gallery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if g.Slug != "new-website-url" {
		t.Fatalf("slug = %q, want new-website-url", g.Slug)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/galleries/1", nil))
	body := rec.Body.String()
	for _, want := range []string{"Managed by Lightroom", "No redirects are kept", "Republish from Lightroom", `value="new-website-url"`} {
		if !strings.Contains(body, want) {
			t.Errorf("gallery page missing %q", want)
		}
	}
	if strings.Contains(body, `/galleries/1/title`) {
		t.Error("managed gallery rendered an editable title field")
	}
}

func TestGalleryOptionsCanBeResetToDefaults(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	parentID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "parent", Title: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := srv.store.CreateGallery(ctx, model.Gallery{
		ParentID: &parentID, Slug: "custom", Title: "Custom", Status: model.GalleryPublished,
		SortMode: model.SortByFilename, SortDirection: model.SortDescending,
		ShowEXIF: model.VisibilityHide, ShowTitle: model.VisibilityShow, ShowDescription: model.VisibilityHide,
	})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := srv.store.CreateItem(ctx, model.Item{
		GalleryID: id, OriginalPath: "custom/photo.jpg", Filename: "photo.jpg", SortOrder: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/galleries/2/options/reset", nil)
	req.Header.Set("X-Curator-Async", "true")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Gallery options reset") {
		t.Fatalf("reset response = %d %q", rec.Code, rec.Body.String())
	}
	g, err := srv.store.Gallery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if g.SortMode != model.SortDefault || g.SortDirection != model.SortDirectionDefault ||
		g.ShowEXIF != model.VisibilityInherit || g.ShowTitle != model.VisibilityInherit || g.ShowDescription != model.VisibilityInherit {
		t.Fatalf("reset options = %#v", g)
	}
	if g.Title != "Custom" || g.Slug != "custom" || g.Status != model.GalleryPublished || g.ParentID == nil || *g.ParentID != parentID {
		t.Fatalf("reset changed gallery identity or placement: %#v", g)
	}
	item, err := srv.store.Item(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if item.SortOrder != 0 {
		t.Fatalf("item sort order = %d, want 0", item.SortOrder)
	}
}

func TestGalleryCustomOrderCanBeReset(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{
		Slug: "ordered", Title: "Ordered", Type: model.GalleryGrid, Status: model.GalleryDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := srv.store.CreateItem(ctx, model.Item{
		GalleryID: galleryID, OriginalPath: "ordered/a.jpg", Filename: "a.jpg",
		Status: model.ItemPublished, SortOrder: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	h := srv.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/galleries/1", nil))
	if !strings.Contains(rec.Body.String(), `data-custom="true"`) {
		t.Fatal("gallery did not show custom ordering")
	}

	form := url.Values{"mode": {"date"}, "direction": {"desc"}}
	req := httptest.NewRequest("POST", "/galleries/1/order", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Curator-Async", "true")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Ordering updated") {
		t.Fatalf("reset response = %d %q", rec.Code, rec.Body.String())
	}
	item, err := srv.store.Item(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if item.SortOrder != 0 {
		t.Fatalf("sort order = %d, want 0", item.SortOrder)
	}
	gallery, err := srv.store.Gallery(ctx, galleryID)
	if err != nil {
		t.Fatal(err)
	}
	if gallery.SortMode != model.SortByDate || gallery.SortDirection != model.SortDescending {
		t.Fatalf("gallery ordering = %q %q, want date desc", gallery.SortMode, gallery.SortDirection)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/galleries/1", nil))
	if !strings.Contains(rec.Body.String(), `data-custom="false"`) ||
		!strings.Contains(rec.Body.String(), `<option value="date" selected>Date taken</option>`) ||
		!strings.Contains(rec.Body.String(), `<option value="desc" selected>Descending</option>`) {
		t.Fatal("gallery did not return to automatic ordering")
	}
}

func TestGalleryItemsCanBeReordered(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{
		Slug: "arranged", Title: "Arranged", Type: model.GalleryGrid, Status: model.GalleryDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	var itemIDs []int64
	for _, filename := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		itemID, err := srv.store.CreateItem(ctx, model.Item{
			GalleryID: galleryID, OriginalPath: "arranged/" + filename, Filename: filename,
			Status: model.ItemPublished,
		})
		if err != nil {
			t.Fatal(err)
		}
		itemIDs = append(itemIDs, itemID)
	}

	form := url.Values{"item_id": {
		strconv.FormatInt(itemIDs[2], 10), strconv.FormatInt(itemIDs[0], 10), strconv.FormatInt(itemIDs[1], 10),
	}}
	req := httptest.NewRequest("POST", "/galleries/1/reorder", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Curator-Async", "true")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Order updated") {
		t.Fatalf("reorder response = %d %q", rec.Code, rec.Body.String())
	}
	items, err := srv.store.ItemsByGallery(ctx, galleryID)
	if err != nil {
		t.Fatal(err)
	}
	got := []int64{items[0].ID, items[1].ID, items[2].ID}
	want := []int64{itemIDs[2], itemIDs[0], itemIDs[1]}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestDefaultGalleryOrderingAppliesToNewGalleries(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	settings := url.Values{
		"default_gallery_order":          {"filename"},
		"default_gallery_sort_direction": {"desc"},
	}
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(settings.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("settings status = %d, want 303", rec.Code)
	}

	create := url.Values{"title": {"Alphabetical Gallery"}}
	req = httptest.NewRequest("POST", "/galleries", strings.NewReader(create.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303", rec.Code)
	}

	gallery, err := srv.store.Gallery(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if gallery.SortMode != model.SortDefault {
		t.Fatalf("new gallery sort mode = %q, want default", gallery.SortMode)
	}
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Hour)
	firstID, err := srv.store.CreateItem(context.Background(), model.Item{
		GalleryID: gallery.ID, OriginalPath: "alphabetical-gallery/z.jpg", Filename: "z.jpg", TakenAt: &earlier,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := srv.store.CreateItem(context.Background(), model.Item{
		GalleryID: gallery.ID, OriginalPath: "alphabetical-gallery/a.jpg", Filename: "a.jpg", TakenAt: &later,
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := srv.store.ItemsByGallery(context.Background(), gallery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ID != firstID || items[1].ID != secondID {
		t.Fatalf("inherited alphabetical order = %d, %d", items[0].ID, items[1].ID)
	}
	if err := srv.store.SetSetting(context.Background(), "site.default_gallery_order", "date"); err != nil {
		t.Fatal(err)
	}
	items, err = srv.store.ItemsByGallery(context.Background(), gallery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ID != secondID || items[1].ID != firstID {
		t.Fatalf("inherited date order = %d, %d", items[0].ID, items[1].ID)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/galleries/1", nil))
	if !strings.Contains(rec.Body.String(), `<option value="default" selected>Site default: Date taken</option>`) ||
		!strings.Contains(rec.Body.String(), `<option value="default" selected>Site default: Descending</option>`) {
		t.Fatal("gallery did not show inherited system ordering")
	}
}

func TestDateAddedCanBeTheDefaultGalleryOrder(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	form := url.Values{
		"default_gallery_order":          {"date_added"},
		"default_gallery_sort_direction": {"desc"},
	}
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	settings, err := srv.store.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings["site.default_gallery_order"] != "date_added" {
		t.Fatalf("default gallery order = %q, want date_added", settings["site.default_gallery_order"])
	}
	if _, err := srv.store.CreateGallery(context.Background(), model.Gallery{
		Slug: "recent", Title: "Recent", Type: model.GalleryGrid, Status: model.GalleryDraft,
	}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/galleries/1", nil))
	if !strings.Contains(rec.Body.String(), `Site default: Date added`) {
		t.Fatal("gallery did not show inherited date-added ordering")
	}
}

func TestSettingsSavePromptsForBuildWhenThemeChanges(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.themes = []string{"default", "folio"}
	form := url.Values{
		"title":                 {"My Photos"},
		"theme":                 {"folio"},
		"webserver":             {"nginx"},
		"default_gallery_order": {"date"},
	}
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if location := rec.Header().Get("Location"); !strings.Contains(location, "build+site+to+publish+changes") {
		t.Fatalf("redirect = %q, want build prompt", location)
	}
}

func TestCopyrightSettings(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.themes = []string{"default"}
	form := url.Values{
		"title":                 {"My Photos"},
		"copyright_holder":      {"Example Name"},
		"copyright_start_year":  {"2025"},
		"theme":                 {"default"},
		"webserver":             {"nginx"},
		"default_gallery_order": {"date"},
	}
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if location := rec.Header().Get("Location"); !strings.Contains(location, "build+site+to+publish+changes") {
		t.Fatalf("redirect = %q, want build prompt", location)
	}

	settings, err := srv.store.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings["site.copyright_holder"] != "Example Name" || settings["site.copyright_start_year"] != "2025" {
		t.Fatalf("copyright settings = %q, %q", settings["site.copyright_holder"], settings["site.copyright_start_year"])
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))
	if !strings.Contains(rec.Body.String(), `name="copyright_holder" value="Example Name"`) ||
		!strings.Contains(rec.Body.String(), `name="copyright_start_year" value="2025"`) {
		t.Fatal("settings page did not retain copyright settings")
	}
}

func TestPublishingSettingsRemainIndependentFromSite(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	if err := srv.store.SetSetting(ctx, "site.base_url", "https://photos.example.com"); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"feed_enabled": {"on"},
		"webserver":    {"apache"},
		"server_root":  {"/srv/photos"},
	}
	req := httptest.NewRequest("POST", "/settings/publishing", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "build+site+to+publish+changes") {
		t.Fatalf("publishing response = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	siteForm := url.Values{
		"title":                 {"My Photos"},
		"theme":                 {"default"},
		"default_gallery_order": {"date"},
	}
	req = httptest.NewRequest("POST", "/settings", strings.NewReader(siteForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	settings, err := srv.store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings["site.feed_enabled"] != "true" || settings["site.webserver"] != "apache" || settings["site.server_root"] != "/srv/photos" {
		t.Fatalf("publishing settings changed after Site save: %+v", settings)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/settings/publishing", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `href="/settings/publishing" aria-current="page"`) ||
		!strings.Contains(rec.Body.String(), `name="feed_enabled" checked`) ||
		!strings.Contains(rec.Body.String(), `<option value="apache" selected>`) {
		t.Fatal("publishing settings page did not retain its values")
	}
}

func TestSettingsSavePromptsForBuildForSystemGalleryDefault(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.themes = []string{"default"}
	form := url.Values{
		"title":                 {"My Photos"},
		"theme":                 {"default"},
		"webserver":             {"nginx"},
		"default_gallery_order": {"filename"},
	}
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if location := rec.Header().Get("Location"); !strings.Contains(location, "build+site+to+publish+changes") {
		t.Fatalf("redirect = %q, want build prompt for inherited ordering", location)
	}
}

func TestLensMetadataSettings(t *testing.T) {
	srv, _ := newTestServer(t)
	form := url.Values{
		"use_lightroom_lens_profile": {"on"},
		"mapping_camera":             {"FUJIFILM XF10"},
		"mapping_lens":               {"FUJINON 18.5mm F2.8"},
		"lens_name_existing":         {"45.0 mm f/2.8", "Nikkor 28mm f/3.5 AI-s"},
		"lens_name_canonical":        {"Nikkor 45mm f/2.8P AI-s", "Nikkor 28mm f/3.5 AI"},
		"facet_camera":               {"on"},
		"tag_visibility":             {"hide_selected"},
		"selected_tag":               {"Private", "private", "Public"},
		"tag_browse_enabled":         {"on"},
		"facet_pagination_enabled":   {"on"},
		"facet_page_size":            {"60"},
	}
	req := httptest.NewRequest("POST", "/settings/metadata", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("settings status = %d, want 303", rec.Code)
	}

	settings, err := srv.store.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings["metadata.use_lightroom_lens_profile"] != "true" {
		t.Error("XMP lens fallback was not enabled")
	}
	if settings["metadata.lens_mappings"] != "FUJIFILM XF10 = FUJINON 18.5mm F2.8" {
		t.Errorf("lens mappings = %q", settings["metadata.lens_mappings"])
	}
	if settings["metadata.lens_name_mappings"] != "45.0 mm f/2.8 = Nikkor 45mm f/2.8P AI-s\nNikkor 28mm f/3.5 AI-s = Nikkor 28mm f/3.5 AI" {
		t.Errorf("lens-name mappings = %q", settings["metadata.lens_name_mappings"])
	}
	if settings["metadata.facet_pagination_enabled"] != "true" || settings["metadata.facet_page_size"] != "60" {
		t.Fatalf("pagination settings = %q, %q", settings["metadata.facet_pagination_enabled"], settings["metadata.facet_page_size"])
	}
	if settings["metadata.tag_visibility"] != "hide_selected" || settings["metadata.tag_selection"] != "Private\nPublic" {
		t.Fatalf("tag settings = %q, %q", settings["metadata.tag_visibility"], settings["metadata.tag_selection"])
	}
	facets, err := srv.store.FacetConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(facets) < 3 || !facets[0].Enabled || facets[1].Enabled || !facets[2].Enabled {
		t.Fatalf("public browse pages = %+v", facets)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/settings/metadata", nil))
	if !strings.Contains(rec.Body.String(), `name="use_lightroom_lens_profile" checked`) ||
		!strings.Contains(rec.Body.String(), `name="mapping_camera" value="FUJIFILM XF10"`) ||
		!strings.Contains(rec.Body.String(), `name="mapping_lens" value="FUJINON 18.5mm F2.8"`) ||
		!strings.Contains(rec.Body.String(), `name="lens_name_existing" value="45.0 mm f/2.8"`) ||
		!strings.Contains(rec.Body.String(), `name="lens_name_canonical" value="Nikkor 45mm f/2.8P AI-s"`) ||
		!strings.Contains(rec.Body.String(), `name="facet_camera" checked`) ||
		!strings.Contains(rec.Body.String(), `<option value="hide_selected" selected>Show all except selected</option>`) ||
		!strings.Contains(rec.Body.String(), `name="tag_browse_enabled" checked`) ||
		!strings.Contains(rec.Body.String(), `name="facet_pagination_enabled" checked`) ||
		!strings.Contains(rec.Body.String(), `name="facet_page_size" value="60"`) ||
		!strings.Contains(rec.Body.String(), `<strong>Camera</strong><small>Generate a public Camera browse page.</small>`) {
		t.Fatal("settings page did not retain lens metadata settings")
	}
}

func TestXMPProfileRows(t *testing.T) {
	usages := []store.XMPProfileUsage{
		{Profile: "Profile A", Camera: "Camera A", Count: 2},
		{Profile: "Profile A", Camera: "Camera B", Count: 1},
		{Profile: "Profile B", Camera: "Camera C", Count: 3},
	}
	mappings := map[string]string{"Camera A": "Mapped lens"}

	rows := xmpProfileRows(usages, mappings, true)
	if len(rows) != 2 || rows[0].Count != 3 || rows[0].Status != "Partially overridden" ||
		rows[0].Cameras != "Camera A, Camera B" || rows[1].Status != "Used" {
		t.Fatalf("enabled profile rows = %+v", rows)
	}

	rows = xmpProfileRows(usages, mappings, false)
	if rows[0].Status != "Disabled" || rows[1].Status != "Disabled" {
		t.Fatalf("disabled profile rows = %+v", rows)
	}
}

func TestCameraLensSuggestions(t *testing.T) {
	clues := []store.CameraLensClue{
		{Camera: "FUJIFILM XF10", Focal: "18.5 mm", MaxApertureAPEX: "297/100", Count: 27},
		{Camera: "FUJIFILM GFX 50R", XMPProfile: "Voigtlander 12mm", Count: 4},
		{Camera: "FUJIFILM GFX 50R", XMPProfile: "Voigtlander 15mm", Count: 3},
	}

	suggestions := cameraLensSuggestions(clues, true)
	if got := suggestions["FUJIFILM XF10"]; got.Lens != "FUJIFILM XF10 18.5mm f/2.8" ||
		!strings.Contains(got.Evidence, "27 photos") || !strings.Contains(got.Evidence, "max f/2.8") {
		t.Fatalf("XF10 suggestion = %+v", got)
	}
	if got := suggestions["FUJIFILM GFX 50R"]; got.Lens != "" ||
		!strings.Contains(got.Evidence, "2 different XMP lens names") {
		t.Fatalf("ambiguous GFX suggestion = %+v", got)
	}
}

func TestCameraLensSuggestionExplainsXMPFallbackState(t *testing.T) {
	clues := []store.CameraLensClue{{
		Camera: "FUJIFILM X100S", XMPProfile: "Fujifilm X100S", Count: 12,
	}}

	enabled := cameraLensSuggestions(clues, true)["FUJIFILM X100S"]
	if enabled.XMPName != "Fujifilm X100S" || !enabled.XMPFallbackActive || enabled.CanEnableFallback {
		t.Errorf("enabled suggestion = %+v", enabled)
	}
	disabled := cameraLensSuggestions(clues, false)["FUJIFILM X100S"]
	if disabled.XMPName != "Fujifilm X100S" || disabled.XMPFallbackActive || !disabled.CanEnableFallback {
		t.Errorf("disabled suggestion = %+v", disabled)
	}
}

func TestInvalidLensMappingIsRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	form := url.Values{"mapping_camera": {""}, "mapping_lens": {"FUJINON 18.5mm F2.8"}}
	req := httptest.NewRequest("POST", "/settings/metadata", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "needs+a+camera+and+lens") {
		t.Fatalf("invalid mapping response = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	settings, err := srv.store.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings["metadata.lens_mappings"] != "" {
		t.Errorf("invalid mapping was stored as %q", settings["metadata.lens_mappings"])
	}
}

func TestMetadataSettingsSuggestCamerasWithoutLens(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "missing-lens", Title: "Missing lens"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := srv.store.CreateItem(ctx, model.Item{
			GalleryID: galleryID, OriginalPath: "missing-lens/photo.jpg", Filename: "photo.jpg",
			Camera: "FUJIFILM XF10", Status: model.ItemPublished,
		}); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/settings/metadata", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.Count(rec.Body.String(), `name="mapping_camera" value="FUJIFILM XF10"`); got != 1 {
		t.Fatalf("suggested camera rows = %d, want 1", got)
	}

	form := url.Values{"mapping_camera": {"FUJIFILM XF10"}, "mapping_lens": {""}}
	req := httptest.NewRequest("POST", "/settings/metadata", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "Metadata+settings+saved") {
		t.Fatalf("blank suggestion response = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestMetadataSettingsMakeDisabledXMPFallbackActionable(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	galleryID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "xmp-lens", Title: "XMP lens"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CreateItem(ctx, model.Item{
		GalleryID: galleryID, OriginalPath: "xmp-lens/photo.jpg", Filename: "photo.jpg",
		Camera: "FUJIFILM X100S", XMPLens: "Fujifilm X100S", Status: model.ItemPublished,
	}); err != nil {
		t.Fatal(err)
	}

	render := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/metadata", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		return rec.Body.String()
	}
	body := render()
	if !strings.Contains(body, `Found in XMP: <strong>Fujifilm X100S</strong>`) ||
		!strings.Contains(body, `class="xmp-enable secondary-button" type="button">Enable XMP lens fallback</button>`) {
		t.Fatal("disabled XMP evidence did not show its value and enable action")
	}

	if err := srv.store.SetSetting(ctx, "metadata.lens_mappings", "FUJIFILM X100S = FUJINON 23mm F2"); err != nil {
		t.Fatal(err)
	}
	mappedBody := render()
	if strings.Contains(mappedBody, `>Enable XMP lens fallback</button>`) {
		t.Fatal("mapped camera should not offer to enable XMP fallback")
	}
	if !strings.Contains(mappedBody, `Found in XMP: <strong>Fujifilm X100S</strong>`) ||
		!strings.Contains(mappedBody, `The mapping takes precedence.`) {
		t.Fatal("mapped camera did not retain its XMP evidence and precedence explanation")
	}
}

func TestBuildButtonInvokesBuild(t *testing.T) {
	srv, built := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/build", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("build status = %d, want 303", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "/" {
		t.Fatalf("build redirect = %q, want dashboard without flash", location)
	}
	select {
	case <-built:
	case <-time.After(2 * time.Second):
		t.Error("build function was not invoked")
	}
}

func TestBuildButtonQueuesWhilePublishIsRunning(t *testing.T) {
	srv, _ := newTestServer(t)
	if !srv.builds.begin() {
		t.Fatal("publish did not begin")
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/build", nil))

	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "Publish+queued") {
		t.Fatalf("queue response = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if status := srv.builds.snapshot(); !status.Running || !status.Pending {
		t.Fatalf("queue status = %#v", status)
	}
}

func TestBasePathRouting(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.New(tmp, filepath.Join(tmp, "output"))
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, cfg, Options{BasePath: "/admin"})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/admin/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("base-path dashboard status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `href="/admin/settings"`) {
		t.Error("links not prefixed with base path")
	}
}
