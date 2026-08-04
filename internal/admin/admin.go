// Package admin serves Curator's private, server-rendered management UI. It runs
// Curator's own HTTP server and reuses the same ingest and build packages the
// CLI uses, so galleries, uploads, settings, and publishing are all driven from
// the browser. It is designed to run behind a reverse proxy: bind to localhost,
// optionally under a base path, with TLS and auth terminated upstream.
package admin

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/tkjaer/curator/internal/build"
	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/deploy"
	"github.com/tkjaer/curator/internal/publishapi"
	"github.com/tkjaer/curator/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

// BuildFunc runs a site build, reporting progress, and returns a summary.
type BuildFunc func(ctx context.Context, onProgress func(build.Progress)) (build.Report, error)

// DeployFunc publishes a generated site to an rsync destination.
type DeployFunc func(ctx context.Context, target string, delete bool) error

// Options configure the admin server for direct or proxied operation.
type Options struct {
	BasePath   string
	TrustProxy bool
	Build      BuildFunc
	Deploy     DeployFunc
	Themes     []string
}

// Server is the admin HTTP application.
type Server struct {
	store      *store.Store
	cfg        config.Config
	basePath   string
	build      BuildFunc
	deploy     DeployFunc
	tmpl       *template.Template
	themes     []string
	publishAPI *publishapi.API

	trustProxy       bool
	authEnabled      bool
	passwordHash     string
	publishTokenHash string
	secret           []byte
	throttle         *throttle
	loginSem         chan struct{}
	builds           *buildStatus
}

// New parses templates, loads auth settings, and returns an admin server.
func New(st *store.Store, cfg config.Config, opts Options) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	deploySite := opts.Deploy
	if deploySite == nil {
		deploySite = func(ctx context.Context, target string, delete bool) error {
			return deploy.Rsync(ctx, cfg.OutputDir, deploy.Options{Target: target, Delete: delete})
		}
	}
	s := &Server{
		store:      st,
		cfg:        cfg,
		basePath:   strings.TrimRight(opts.BasePath, "/"),
		build:      opts.Build,
		deploy:     deploySite,
		tmpl:       tmpl,
		themes:     opts.Themes,
		trustProxy: opts.TrustProxy,
		throttle:   newThrottle(),
		loginSem:   make(chan struct{}, throttleMaxConcurrent),
		builds:     newBuildStatus(),
	}
	if err := s.loadAuth(context.Background()); err != nil {
		return nil, err
	}
	s.publishAPI = publishapi.New(st, cfg, s.publishTokenHash)
	s.publishAPI.SetBuildTrigger(s.queueBuild)
	return s, nil
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+s.path("/"), s.handleDashboard)
	mux.HandleFunc("GET "+s.path("/login"), s.handleLoginForm)
	mux.HandleFunc("POST "+s.path("/login"), s.handleLogin)
	mux.HandleFunc("POST "+s.path("/logout"), s.handleLogout)
	mux.HandleFunc("POST "+s.path("/galleries"), s.handleCreateGallery)
	mux.HandleFunc("GET "+s.path("/galleries/{id}"), s.handleGallery)
	mux.HandleFunc("POST "+s.path("/galleries/{id}/upload"), s.handleUpload)
	mux.HandleFunc("POST "+s.path("/galleries/{id}/status"), s.handleGalleryStatus)
	mux.HandleFunc("POST "+s.path("/galleries/{id}/presentation"), s.handleGalleryPresentation)
	mux.HandleFunc("POST "+s.path("/galleries/{id}/delete"), s.handleDeleteGallery)
	mux.HandleFunc("POST "+s.path("/galleries/{id}/move"), s.handleMoveGallery)
	mux.HandleFunc("POST "+s.path("/galleries/{id}/order"), s.handleGalleryItemOrder)
	mux.HandleFunc("POST "+s.path("/items/{id}/update"), s.handleItemUpdate)
	mux.HandleFunc("POST "+s.path("/items/{id}/cover"), s.handleItemCover)
	mux.HandleFunc("POST "+s.path("/items/{id}/move"), s.handleItemMove)
	mux.HandleFunc("POST "+s.path("/items/{id}/delete"), s.handleItemDelete)
	mux.HandleFunc("POST "+s.path("/galleries/{id}/access"), s.handleGalleryAccess)
	mux.HandleFunc("POST "+s.path("/galleries/{id}/blocks"), s.handleCreateBlock)
	mux.HandleFunc("POST "+s.path("/blocks/{id}/update"), s.handleUpdateBlock)
	mux.HandleFunc("POST "+s.path("/blocks/{id}/move"), s.handleMoveBlock)
	mux.HandleFunc("POST "+s.path("/blocks/{id}/delete"), s.handleDeleteBlock)
	mux.HandleFunc("GET "+s.path("/access"), s.handleAccess)
	mux.HandleFunc("POST "+s.path("/access"), s.handleCreateAccessUser)
	mux.HandleFunc("POST "+s.path("/access/{id}/delete"), s.handleDeleteAccessUser)
	mux.HandleFunc("GET "+s.path("/settings"), s.handleSettings)
	mux.HandleFunc("POST "+s.path("/settings/gallery-presentation/reset"), s.handleResetGalleryPresentation)
	mux.HandleFunc("POST "+s.path("/settings"), s.handleSaveSettings)
	mux.HandleFunc("GET "+s.path("/settings/metadata"), s.handleMetadataSettings)
	mux.HandleFunc("POST "+s.path("/settings/metadata"), s.handleSaveMetadataSettings)
	mux.HandleFunc("GET "+s.path("/settings/publishing"), s.handlePublishingSettings)
	mux.HandleFunc("POST "+s.path("/settings/publishing"), s.handleSavePublishingSettings)
	mux.HandleFunc("POST "+s.path("/settings/publishing/token"), s.handleCreatePublishToken)
	mux.HandleFunc("POST "+s.path("/settings/password"), s.handlePassword)
	mux.HandleFunc("POST "+s.path("/build"), s.handleBuild)
	mux.HandleFunc("GET "+s.path("/build/status"), s.handleBuildStatus)
	mux.HandleFunc("POST "+s.path("/rescan"), s.handleRescan)

	media := s.path("/media/")
	mux.Handle("GET "+media, http.StripPrefix(media, http.FileServer(http.Dir(s.cfg.OriginalsDir()))))

	apiPrefix := s.path("/api/v1")
	root := http.NewServeMux()
	root.Handle(apiPrefix+"/", http.StripPrefix(apiPrefix, s.publishAPI.Handler()))
	root.Handle("/", s.withAuth(mux))
	return root
}

// path prefixes a route with the configured base path.
func (s *Server) path(p string) string {
	return s.basePath + p
}

// link builds a link, joining the base path with the given segments.
func (s *Server) link(parts ...string) string {
	return s.basePath + "/" + strings.TrimLeft(path.Join(parts...), "/")
}

// page is the common template context.
type page struct {
	BasePath    string
	Title       string
	Flash       string
	CSRF        string
	Authed      bool
	AuthEnabled bool
	Data        any
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name, title, flash string, data any) {
	sess := sessionFrom(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, page{
		BasePath:    s.basePath,
		Title:       title,
		Flash:       flash,
		CSRF:        sess.CSRF,
		Authed:      sess.Auth,
		AuthEnabled: s.authEnabled,
		Data:        data,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// redirect sends the browser to an admin URL with an optional flash message.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to, flash string) {
	if r.Header.Get("X-Curator-Async") == "true" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": flash, "redirect": to})
		return
	}
	if flash != "" {
		if strings.Contains(to, "?") {
			to += "&msg=" + url.QueryEscape(flash)
		} else {
			to += "?msg=" + url.QueryEscape(flash)
		}
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
