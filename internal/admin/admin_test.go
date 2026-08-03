package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
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

func TestDashboardRendersCollapsibleGalleryTree(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	parentID, err := srv.store.CreateGallery(ctx, model.Gallery{Slug: "2026", Title: "2026"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CreateGallery(ctx, model.Gallery{ParentID: &parentID, Slug: "summer", Title: "Summer"}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="gallery-tree"`,
		`data-gallery-id="1" data-parent-id="" data-depth="0"`,
		`aria-expanded="false" aria-label="Expand 2026"`,
		`data-gallery-id="2" data-parent-id="1" data-depth="1"`,
		`curator-expanded-galleries:`,
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
		`<details class="gallery-options">`,
		`<summary>Edit gallery settings</summary>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("gallery missing %q", want)
		}
	}
}

func TestGalleryDefaultsCanBeSavedAndApplied(t *testing.T) {
	srv, _ := newTestServer(t)
	handler := srv.Handler()

	settings := url.Values{
		"default_gallery_order":     {"date"},
		"default_gallery_published": {"on"},
		"default_gallery_show_exif": {"on"},
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
	if len(galleries) != 1 || galleries[0].Status != model.GalleryPublished || !galleries[0].ShowEXIF || galleries[0].PublishedAt == nil {
		t.Fatalf("gallery defaults not applied: %#v", galleries)
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
	if status.Running || status.Error != "" || status.Stage != "Deploying" {
		t.Fatalf("publish status = %#v", status)
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

func TestBuildStatusIDsDistinguishBuilds(t *testing.T) {
	status := newBuildStatus()
	if !status.begin() {
		t.Fatal("first build did not begin")
	}
	first := status.snapshot().BuildID
	status.finish(build.Report{FeedUpdated: true}, nil)
	if !status.snapshot().FeedUpdated {
		t.Fatal("Atom feed update missing from build status")
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
	if !strings.Contains(rec.Body.String(), `<option value="default" selected>Default: Date taken</option>`) ||
		!strings.Contains(rec.Body.String(), `<option value="default" selected>Default: Descending</option>`) {
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
	if !strings.Contains(rec.Body.String(), `Default: Date added`) {
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
		"facet_camera":               {"on"},
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
		t.Error("Lightroom profile fallback was not enabled")
	}
	if settings["metadata.lens_mappings"] != "FUJIFILM XF10 = FUJINON 18.5mm F2.8" {
		t.Errorf("lens mappings = %q", settings["metadata.lens_mappings"])
	}
	if settings["metadata.facet_pagination_enabled"] != "true" || settings["metadata.facet_page_size"] != "60" {
		t.Fatalf("pagination settings = %q, %q", settings["metadata.facet_pagination_enabled"], settings["metadata.facet_page_size"])
	}
	facets, err := srv.store.FacetConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(facets) < 2 || !facets[0].Enabled || facets[1].Enabled {
		t.Fatalf("public browse pages = %+v", facets)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/settings/metadata", nil))
	if !strings.Contains(rec.Body.String(), `name="use_lightroom_lens_profile" checked`) ||
		!strings.Contains(rec.Body.String(), `name="mapping_camera" value="FUJIFILM XF10"`) ||
		!strings.Contains(rec.Body.String(), `name="mapping_lens" value="FUJINON 18.5mm F2.8"`) ||
		!strings.Contains(rec.Body.String(), `name="facet_camera" checked`) ||
		!strings.Contains(rec.Body.String(), `name="facet_pagination_enabled" checked`) ||
		!strings.Contains(rec.Body.String(), `name="facet_page_size" value="60"`) ||
		!strings.Contains(rec.Body.String(), `Generate a <strong>Camera</strong> browse page`) {
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

	suggestions := cameraLensSuggestions(clues)
	if got := suggestions["FUJIFILM XF10"]; got.Lens != "FUJIFILM XF10 18.5mm f/2.8" ||
		!strings.Contains(got.Evidence, "27 photos") || !strings.Contains(got.Evidence, "max f/2.8") {
		t.Fatalf("XF10 suggestion = %+v", got)
	}
	if got := suggestions["FUJIFILM GFX 50R"]; got.Lens != "" ||
		!strings.Contains(got.Evidence, "2 XMP profiles") {
		t.Fatalf("ambiguous GFX suggestion = %+v", got)
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
