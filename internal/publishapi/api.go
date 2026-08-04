package publishapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/ingest"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/slug"
	"github.com/tkjaer/curator/internal/store"
)

const maxUploadBytes = 1 << 30

type API struct {
	store     *store.Store
	cfg       config.Config
	tokenMu   sync.RWMutex
	tokenHash string
	build     func() error
}

func New(st *store.Store, cfg config.Config, tokenHash string) *API {
	return &API{store: st, cfg: cfg, tokenHash: tokenHash}
}

func (a *API) SetTokenHash(tokenHash string) {
	a.tokenMu.Lock()
	a.tokenHash = tokenHash
	a.tokenMu.Unlock()
}

func (a *API) SetBuildTrigger(build func() error) {
	a.build = build
}

func (a *API) configuredTokenHash() string {
	a.tokenMu.RLock()
	defer a.tokenMu.RUnlock()
	return a.tokenHash
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.handleCapabilities)
	mux.HandleFunc("GET /galleries", a.handleGalleries)
	mux.HandleFunc("POST /galleries", a.handleCreateGallery)
	mux.HandleFunc("PUT /sync/galleries/{externalID}", a.handleUpsertGallery)
	mux.HandleFunc("POST /sync/galleries/{id}/photos/{externalID}", a.handleUpsertPhoto)
	mux.HandleFunc("PUT /sync/galleries/{id}/order", a.handleItemOrder)
	mux.HandleFunc("DELETE /sync/galleries/{id}", a.handleDeleteGallery)
	mux.HandleFunc("DELETE /sync/photos/{id}", a.handleDeletePhoto)
	mux.HandleFunc("POST /sync/build", a.handleBuild)
	mux.HandleFunc("POST /galleries/{id}/photos", a.handleUploadPhoto)
	return a.withToken(mux)
}

func (a *API) withToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHash := a.configuredTokenHash()
		if tokenHash == "" {
			writeError(w, http.StatusServiceUnavailable, "publishing API is not configured")
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		provided := TokenHash(token)
		if token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(tokenHash)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Curator Publish API"`)
			writeError(w, http.StatusUnauthorized, "invalid publishing token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":         "Curator Publish API",
		"version":      1,
		"capabilities": []string{"galleries:list", "galleries:create", "galleries:sync", "photos:upload", "photos:sync", "photos:delete", "photos:order", "build:trigger"},
	})
}

func (a *API) handleGalleries(w http.ResponseWriter, r *http.Request) {
	galleries, err := a.store.Galleries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list galleries")
		return
	}
	items := make([]galleryResponse, 0, len(galleries))
	for _, gallery := range galleries {
		items = append(items, galleryResponse{
			ID: gallery.ID, ParentID: gallery.ParentID, Slug: gallery.Slug,
			Title: gallery.Title, Description: gallery.Description, Status: gallery.Status,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"galleries": items})
}

type galleryResponse struct {
	ID          int64               `json:"id"`
	ParentID    *int64              `json:"parent_id,omitempty"`
	Slug        string              `json:"slug"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Status      model.GalleryStatus `json:"status"`
}

type createGalleryRequest struct {
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	ParentID    *int64 `json:"parent_id"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type syncGalleryRequest struct {
	Title            string `json:"title"`
	Slug             string `json:"slug"`
	ParentExternalID string `json:"parent_external_id"`
	Description      string `json:"description"`
	Status           string `json:"status"`
}

func (a *API) handleUpsertGallery(w http.ResponseWriter, r *http.Request) {
	externalID := strings.TrimSpace(r.PathValue("externalID"))
	if externalID == "" {
		writeError(w, http.StatusBadRequest, "external id is required")
		return
	}
	var input syncGalleryRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	var parentID *int64
	if input.ParentExternalID != "" {
		id, found, err := a.store.ExternalGalleryID(r.Context(), "lightroom", input.ParentExternalID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not resolve parent gallery")
			return
		}
		if !found {
			writeError(w, http.StatusConflict, "parent gallery has not been synchronized")
			return
		}
		parentID = &id
	}
	status, err := a.store.GalleryDefaults(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load gallery defaults")
		return
	}
	if input.Status != "" {
		status = model.GalleryStatus(input.Status)
	}
	if status != model.GalleryDraft && status != model.GalleryUnlisted && status != model.GalleryPublished {
		writeError(w, http.StatusBadRequest, "status must be draft, unlisted, or published")
		return
	}
	gallerySlug := slug.Make(input.Slug)
	if gallerySlug == "" {
		gallerySlug = slug.Make(input.Title)
	}
	id, created, err := a.store.UpsertExternalGallery(r.Context(), "lightroom", externalID, model.Gallery{
		ParentID: parentID, Slug: gallerySlug, Title: input.Title, Description: input.Description,
		Type: model.GalleryGrid, Status: status, SortMode: model.SortDefault,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "could not synchronize gallery: "+err.Error())
		return
	}
	httpStatus := http.StatusOK
	if created {
		httpStatus = http.StatusCreated
	}
	response := map[string]any{"id": id, "slug": gallerySlug}
	if publicURL, err := a.publicGalleryURL(r.Context(), id); err == nil && publicURL != "" {
		response["url"] = publicURL
	}
	writeJSON(w, httpStatus, response)
}

func (a *API) publicGalleryURL(ctx context.Context, galleryID int64) (string, error) {
	settings, err := a.store.Settings(ctx)
	if err != nil {
		return "", err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(settings["site.base_url"]), "/")
	if baseURL == "" {
		return "", nil
	}
	galleries, err := a.store.Galleries(ctx)
	if err != nil {
		return "", err
	}
	byID := make(map[int64]model.Gallery, len(galleries))
	for _, gallery := range galleries {
		byID[gallery.ID] = gallery
	}
	gallery, found := byID[galleryID]
	if !found {
		return "", sql.ErrNoRows
	}
	if gallery.Status != model.GalleryPublished && gallery.Status != model.GalleryUnlisted {
		return "", nil
	}
	segments := []string{gallery.Slug}
	for gallery.ParentID != nil {
		parent, found := byID[*gallery.ParentID]
		if !found {
			return "", fmt.Errorf("gallery parent %d not found", *gallery.ParentID)
		}
		if parent.Status != model.GalleryPublished && parent.Status != model.GalleryUnlisted {
			return "", nil
		}
		segments = append([]string{parent.Slug}, segments...)
		gallery = parent
	}
	return baseURL + "/" + strings.Join(segments, "/") + "/", nil
}

func (a *API) handleCreateGallery(w http.ResponseWriter, r *http.Request) {
	var input createGalleryRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if input.Slug == "" {
		input.Slug = slug.Make(input.Title)
	} else {
		input.Slug = slug.Make(input.Slug)
	}
	status, err := a.store.GalleryDefaults(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load gallery defaults")
		return
	}
	if input.Status != "" {
		status = model.GalleryStatus(input.Status)
	}
	if status != model.GalleryDraft && status != model.GalleryUnlisted && status != model.GalleryPublished {
		writeError(w, http.StatusBadRequest, "status must be draft, unlisted, or published")
		return
	}
	id, err := a.store.CreateGallery(r.Context(), model.Gallery{
		ParentID: input.ParentID, Slug: input.Slug, Title: input.Title,
		Description: input.Description, Type: model.GalleryGrid, Status: status,
		SortMode: model.SortDefault,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "could not create gallery: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "slug": input.Slug})
}

func (a *API) handleUploadPhoto(w http.ResponseWriter, r *http.Request) {
	galleryID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid gallery id")
		return
	}
	gallery, err := a.store.Gallery(r.Context(), galleryID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "gallery not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load gallery")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	var sidecarFile io.Reader
	if sidecar, _, sidecarErr := r.FormFile("sidecar"); sidecarErr == nil {
		sidecarFile = sidecar
		defer sidecar.Close()
	} else if !errors.Is(sidecarErr, http.ErrMissingFile) {
		writeError(w, http.StatusBadRequest, "invalid sidecar upload")
		return
	}

	itemID, err := ingest.ImportUploadIDWithSidecar(r.Context(), a.store, a.cfg, gallery.ID, gallery.Slug, header.Filename, file, sidecarFile)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("could not import photo: %v", err))
		return
	}
	caption := strings.TrimSpace(r.FormValue("caption"))
	if caption != "" {
		if err := a.store.UpdateItemFields(r.Context(), itemID, caption, model.ItemPublished, false); err != nil {
			writeError(w, http.StatusInternalServerError, "photo imported but caption could not be saved")
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": itemID, "filename": header.Filename})
}

func (a *API) handleUpsertPhoto(w http.ResponseWriter, r *http.Request) {
	galleryID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid gallery id")
		return
	}
	externalID := strings.TrimSpace(r.PathValue("externalID"))
	if externalID == "" {
		writeError(w, http.StatusBadRequest, "external id is required")
		return
	}
	gallery, err := a.store.Gallery(r.Context(), galleryID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "gallery not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load gallery")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	var sidecarFile io.Reader
	if sidecar, _, sidecarErr := r.FormFile("sidecar"); sidecarErr == nil {
		sidecarFile = sidecar
		defer sidecar.Close()
	} else if !errors.Is(sidecarErr, http.ErrMissingFile) {
		writeError(w, http.StatusBadRequest, "invalid sidecar upload")
		return
	}

	itemID, found, err := a.store.ExternalItemID(r.Context(), "lightroom", externalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not resolve synchronized photo")
		return
	}
	status := http.StatusOK
	storagePath := filepath.ToSlash(filepath.Join("lightroom", strconv.FormatInt(gallery.ID, 10)))
	storageName := synchronizedStorageName(externalID, header.Filename)
	if found {
		item, err := a.store.Item(r.Context(), itemID)
		if err != nil {
			writeError(w, http.StatusConflict, "synchronized photo no longer exists")
			return
		}
		if item.GalleryID != gallery.ID {
			writeError(w, http.StatusConflict, "synchronized photo belongs to another gallery")
			return
		}
		if err := ingest.ReplaceUploadWithSidecarAt(r.Context(), a.store, a.cfg, itemID, gallery.ID, storagePath, header.Filename, storageName, file, sidecarFile); err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("could not replace photo: %v", err))
			return
		}
	} else {
		itemID, err = ingest.ImportUploadIDWithSidecarAt(r.Context(), a.store, a.cfg, gallery.ID, storagePath, header.Filename, storageName, file, sidecarFile)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("could not import photo: %v", err))
			return
		}
		if err := a.store.SetExternalItem(r.Context(), "lightroom", externalID, itemID); err != nil {
			_ = a.store.DeleteItem(r.Context(), itemID)
			writeError(w, http.StatusInternalServerError, "photo imported but could not be synchronized")
			return
		}
		status = http.StatusCreated
	}
	caption := strings.TrimSpace(r.FormValue("caption"))
	if err := a.store.UpdateItemFields(r.Context(), itemID, caption, model.ItemPublished, false); err != nil {
		writeError(w, http.StatusInternalServerError, "photo synchronized but caption could not be saved")
		return
	}
	item, err := a.store.Item(r.Context(), itemID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "photo synchronized but lens metadata could not be loaded")
		return
	}
	lightroomLens := strings.TrimSpace(r.FormValue("lens"))
	settings, err := a.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "photo synchronized but lens settings could not be loaded")
		return
	}
	lensPolicy, err := ingest.LensPolicyFromSettings(settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "photo synchronized but lens settings are invalid")
		return
	}
	effectiveLens := lensPolicy.Resolve(item.Camera, item.EmbeddedLens, lightroomLens, item.SidecarLens, item.XMPLens)
	if err := a.store.SetItemLightroomLens(r.Context(), itemID, lightroomLens, effectiveLens); err != nil {
		writeError(w, http.StatusInternalServerError, "photo synchronized but lens metadata could not be saved")
		return
	}
	response := map[string]any{"id": itemID, "filename": header.Filename}
	if publicURL, err := a.publicGalleryURL(r.Context(), gallery.ID); err == nil && publicURL != "" {
		photoSlug := slug.Make(strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)))
		response["url"] = publicURL + "#photo-" + photoSlug
	}
	writeJSON(w, status, response)
}

func synchronizedStorageName(externalID, filename string) string {
	hash := sha256.Sum256([]byte(externalID))
	extension := strings.ToLower(filepath.Ext(filename))
	return fmt.Sprintf("%x%s", hash[:16], extension)
}

func (a *API) handleDeletePhoto(w http.ResponseWriter, r *http.Request) {
	itemID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid photo id")
		return
	}
	owned, err := a.store.IsExternalItem(r.Context(), "lightroom", itemID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not verify synchronized photo")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "synchronized photo not found")
		return
	}
	item, err := a.store.Item(r.Context(), itemID)
	if err != nil {
		writeError(w, http.StatusNotFound, "photo not found")
		return
	}
	if err := a.store.DeleteItem(r.Context(), itemID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete photo")
		return
	}
	path := filepath.Join(a.cfg.OriginalsDir(), filepath.FromSlash(item.OriginalPath))
	_ = os.Remove(path)
	_ = os.Remove(strings.TrimSuffix(path, filepath.Ext(path)) + ".xmp")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDeleteGallery(w http.ResponseWriter, r *http.Request) {
	galleryID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid gallery id")
		return
	}
	owned, err := a.store.IsExternalGallery(r.Context(), "lightroom", galleryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not verify synchronized gallery")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "synchronized gallery not found")
		return
	}
	treeOwned, err := a.store.ExternalGalleryTreeOwned(r.Context(), "lightroom", galleryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not verify synchronized gallery contents")
		return
	}
	if !treeOwned {
		writeError(w, http.StatusConflict, "gallery contains content not owned by Lightroom")
		return
	}
	items, err := a.store.ItemsInGalleryTree(r.Context(), galleryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load synchronized gallery items")
		return
	}
	if err := a.store.DeleteGallery(r.Context(), galleryID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete gallery")
		return
	}
	for _, item := range items {
		path := filepath.Join(a.cfg.OriginalsDir(), filepath.FromSlash(item.OriginalPath))
		_ = os.Remove(path)
		_ = os.Remove(strings.TrimSuffix(path, filepath.Ext(path)) + ".xmp")
	}
	w.WriteHeader(http.StatusNoContent)
}

type itemOrderRequest struct {
	ItemIDs []int64 `json:"item_ids"`
}

func (a *API) handleItemOrder(w http.ResponseWriter, r *http.Request) {
	galleryID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid gallery id")
		return
	}
	var input itemOrderRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := a.store.SetExternalItemOrder(r.Context(), "lightroom", galleryID, input.ItemIDs); err != nil {
		writeError(w, http.StatusConflict, "could not synchronize photo order: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleBuild(w http.ResponseWriter, _ *http.Request) {
	if a.build == nil {
		writeError(w, http.StatusServiceUnavailable, "build is not available")
		return
	}
	if err := a.build(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
