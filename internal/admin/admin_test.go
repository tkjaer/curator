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
