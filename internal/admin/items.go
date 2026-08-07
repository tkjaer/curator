package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tkjaer/curator/internal/ingest"
	"github.com/tkjaer/curator/internal/model"
)

func (s *Server) handleItemUpdate(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemOr404(w, r)
	if !ok {
		return
	}
	title := r.FormValue("title")
	description := r.FormValue("description")
	caption := r.FormValue("caption")
	status := model.ItemStatus(r.FormValue("status"))
	highlighted := r.FormValue("highlight") == "on"
	manualCamera := strings.TrimSpace(r.FormValue("manual_camera"))
	manualLens := strings.TrimSpace(r.FormValue("manual_lens"))
	tagValues := strings.FieldsFunc(r.FormValue("tags"), func(r rune) bool {
		return r == ',' || r == ';'
	})

	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update photo")
		return
	}
	policy, err := ingest.LensPolicyFromSettings(settings)
	if err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update photo")
		return
	}
	effectiveCamera := ingest.ResolveCamera(it.EmbeddedCamera, manualCamera)
	effectiveLens := policy.Resolve(effectiveCamera, it.EmbeddedLens, it.LightroomLens, it.SidecarLens, it.XMPLens, manualLens)
	if err := s.store.UpdateItemPresentation(r.Context(), it.ID, title, description, caption, status, highlighted, manualCamera, effectiveCamera, manualLens, effectiveLens); err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update photo")
		return
	}
	lightroomManaged, err := s.store.IsExternalItem(r.Context(), "lightroom", it.ID)
	if err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update photo")
		return
	}
	if lightroomManaged {
		err = s.store.ReplaceItemUserTags(r.Context(), it.ID, tagValues)
	} else {
		err = s.store.ReplaceItemEditableTags(r.Context(), it.ID, tagValues)
	}
	if err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update photo")
		return
	}
	var tags []model.Tag
	if lightroomManaged {
		tags, err = s.store.ItemManualTags(r.Context(), it.ID)
	} else {
		tags, err = s.store.ItemUserTags(r.Context(), it.ID)
	}
	if err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update photo")
		return
	}
	normalizedTags := make([]string, 0, len(tags))
	for _, tag := range tags {
		normalizedTags = append(normalizedTags, tag.Value)
	}
	if r.Header.Get("X-Curator-Async") == "true" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message": "Photo updated", "redirect": s.galleryLink(it.GalleryID), "resolvedCamera": effectiveCamera, "resolvedLens": effectiveLens, "tags": strings.Join(normalizedTags, ", "),
		})
		return
	}
	s.redirect(w, r, s.galleryLink(it.GalleryID), "Photo updated")
}

func (s *Server) handleItemCover(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemOr404(w, r)
	if !ok {
		return
	}
	g, err := s.store.Gallery(r.Context(), it.GalleryID)
	if err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update cover")
		return
	}
	if g.CoverItemID != nil && *g.CoverItemID == it.ID {
		if err := s.store.SetGalleryCover(r.Context(), it.GalleryID, nil); err != nil {
			s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update cover")
			return
		}
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Cover removed")
		return
	}
	id := it.ID
	if err := s.store.SetGalleryCover(r.Context(), it.GalleryID, &id); err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update cover")
		return
	}
	s.redirect(w, r, s.galleryLink(it.GalleryID), "Cover set")
}

func (s *Server) handleItemMove(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemOr404(w, r)
	if !ok {
		return
	}
	up := r.FormValue("dir") == "up"
	if err := s.store.MoveItem(r.Context(), it.GalleryID, it.ID, up); err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not reorder")
		return
	}
	s.redirect(w, r, s.galleryLink(it.GalleryID), "Order updated")
}

func (s *Server) handleGalleryItemOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	mode := model.SortMode(r.FormValue("mode"))
	if mode != model.SortDefault && mode != model.SortByDate && mode != model.SortByDateAdded && mode != model.SortByFilename {
		s.redirect(w, r, s.galleryLink(id), "Unknown ordering")
		return
	}
	direction := model.SortDirection(r.FormValue("direction"))
	if direction == "" {
		direction = model.SortDirectionDefault
	}
	if direction != model.SortDirectionDefault && direction != model.SortAscending && direction != model.SortDescending {
		s.redirect(w, r, s.galleryLink(id), "Unknown ordering direction")
		return
	}
	if err := s.store.SetGalleryItemOrder(r.Context(), id, mode, direction); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not update ordering")
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Ordering updated")
}

func (s *Server) handleGalleryItemReorder(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Invalid photo order")
		return
	}
	itemIDs := make([]int64, 0, len(r.Form["item_id"]))
	for _, value := range r.Form["item_id"] {
		itemID, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			s.redirect(w, r, s.galleryLink(id), "Invalid photo order")
			return
		}
		itemIDs = append(itemIDs, itemID)
	}
	if err := s.store.SetItemOrder(r.Context(), id, itemIDs); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not reorder photos")
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Order updated")
}

func (s *Server) handleItemDelete(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemOr404(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteItem(r.Context(), it.ID); err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not delete photo")
		return
	}
	// Best-effort removal of the original file; a missing file is not fatal.
	_ = os.Remove(filepath.Join(s.cfg.OriginalsDir(), filepath.FromSlash(it.OriginalPath)))
	s.redirect(w, r, s.galleryLink(it.GalleryID), "Photo deleted")
}

func (s *Server) itemOr404(w http.ResponseWriter, r *http.Request) (model.Item, bool) {
	id, ok := parseID(w, r)
	if !ok {
		return model.Item{}, false
	}
	it, err := s.store.Item(r.Context(), id)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return model.Item{}, false
	}
	return it, true
}
