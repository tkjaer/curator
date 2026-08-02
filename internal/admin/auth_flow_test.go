package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/publishapi"
	"github.com/tkjaer/curator/internal/store"
)

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func newAuthServer(t *testing.T, password string) *Server {
	t.Helper()
	tmp := t.TempDir()
	cfg := config.New(tmp, filepath.Join(tmp, "output"))
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "admin.password_hash", string(hash)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func sessionCookieOf(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	return nil
}

// loginPage fetches /login and returns the session cookie and CSRF token.
func loginPage(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", rec.Code)
	}
	cookie := sessionCookieOf(rec.Result())
	if cookie == nil {
		t.Fatal("no session cookie set on /login")
	}
	m := csrfRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("no CSRF token in login form")
	}
	return cookie, m[1]
}

func TestAuthRedirectsWhenEnabled(t *testing.T) {
	srv := newAuthServer(t, "hunter2")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("redirect = %q, want /login", loc)
	}
}

func TestLoginSucceeds(t *testing.T) {
	srv := newAuthServer(t, "hunter2")
	h := srv.Handler()
	cookie, csrf := loginPage(t, h)

	form := url.Values{"_csrf": {csrf}, "password": {"hunter2"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303", rec.Code)
	}
	authCookie := sessionCookieOf(rec.Result())
	if authCookie == nil {
		t.Fatal("no session cookie after login")
	}

	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(authCookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Galleries") {
		t.Errorf("dashboard after login: status %d", rec.Code)
	}
}

func TestWrongPassword(t *testing.T) {
	srv := newAuthServer(t, "hunter2")
	h := srv.Handler()
	cookie, csrf := loginPage(t, h)

	form := url.Values{"_csrf": {csrf}, "password": {"wrong"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "Invalid") {
		t.Errorf("redirect = %q, want an invalid-password message", loc)
	}
}

func TestCSRFRejected(t *testing.T) {
	srv := newAuthServer(t, "hunter2")
	h := srv.Handler()
	cookie, _ := loginPage(t, h)

	form := url.Values{"title": {"No CSRF"}}
	req := httptest.NewRequest("POST", "/galleries", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for missing CSRF token", rec.Code)
	}
}

func TestPublishAPIUsesBearerAuthInsteadOfBrowserSession(t *testing.T) {
	srv := newAuthServer(t, "hunter2")
	const token = "lightroom-token"
	if err := srv.store.SetSetting(context.Background(), "publish.api_token_hash", publishapi.TokenHash(token)); err != nil {
		t.Fatal(err)
	}
	srv.publishTokenHash = publishapi.TokenHash(token)
	srv.publishAPI.SetTokenHash(publishapi.TokenHash(token))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":1`) {
		t.Fatalf("publish API status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
