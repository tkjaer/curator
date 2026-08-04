package admin

import (
	"net/http"
	"os"
	"path/filepath"

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

	if err := s.store.UpdateItemPresentation(r.Context(), it.ID, title, description, caption, status, highlighted); err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not update photo")
		return
	}
	s.redirect(w, r, s.galleryLink(it.GalleryID), "Photo updated")
}

func (s *Server) handleItemCover(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemOr404(w, r)
	if !ok {
		return
	}
	id := it.ID
	if err := s.store.SetGalleryCover(r.Context(), it.GalleryID, &id); err != nil {
		s.redirect(w, r, s.galleryLink(it.GalleryID), "Could not set cover")
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
