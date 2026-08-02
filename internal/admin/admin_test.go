package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkjaer/curator/internal/build"
	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/model"
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

	form := url.Values{"mode": {"date"}}
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

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/galleries/1", nil))
	if !strings.Contains(rec.Body.String(), `data-custom="false"`) ||
		!strings.Contains(rec.Body.String(), `<option value="date" selected>Date taken (default)</option>`) {
		t.Fatal("gallery did not return to automatic ordering")
	}
}

func TestDefaultGalleryOrderAppliesToNewGalleries(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	settings := url.Values{"default_gallery_order": {"filename"}}
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
	if gallery.SortMode != model.SortByFilename {
		t.Fatalf("new gallery sort mode = %q, want filename", gallery.SortMode)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))
	if !strings.Contains(rec.Body.String(), `<option value="filename" selected>Alphabetical</option>`) {
		t.Fatal("settings page did not retain alphabetical default")
	}
}

func TestBuildButtonInvokesBuild(t *testing.T) {
	srv, built := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/build", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("build status = %d, want 303", rec.Code)
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
