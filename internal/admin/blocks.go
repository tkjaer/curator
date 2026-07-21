package admin

import (
	"net/http"
	"strconv"

	"github.com/tkjaer/curator/internal/model"
)

func (s *Server) handleCreateBlock(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	blockType := model.BlockType(r.FormValue("type"))
	if !validBlockType(blockType) {
		s.redirect(w, r, s.galleryLink(id), "Unknown block type")
		return
	}
	if _, err := s.store.CreateBlock(r.Context(), model.Block{
		GalleryID: id, Type: blockType, Content: r.FormValue("content"),
	}); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not add block")
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Block added")
}

func (s *Server) handleUpdateBlock(w http.ResponseWriter, r *http.Request) {
	bl, ok := s.blockOr404(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.redirect(w, r, s.galleryLink(bl.GalleryID), "Could not update block")
		return
	}

	var itemID *int64
	if bl.Type == model.BlockImage {
		if v := r.FormValue("item"); v != "" {
			if iid, err := strconv.ParseInt(v, 10, 64); err == nil {
				itemID = &iid
			}
		}
	}
	if err := s.store.UpdateBlock(r.Context(), bl.ID, r.FormValue("content"), itemID); err != nil {
		s.redirect(w, r, s.galleryLink(bl.GalleryID), "Could not update block")
		return
	}

	if bl.Type == model.BlockGrid {
		items, err := s.store.ItemsByGallery(r.Context(), bl.GalleryID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var selected []int64
		for _, it := range items {
			if r.FormValue("item_"+strconv.FormatInt(it.ID, 10)) == "on" {
				selected = append(selected, it.ID)
			}
		}
		if err := s.store.SetBlockItems(r.Context(), bl.ID, selected); err != nil {
			s.redirect(w, r, s.galleryLink(bl.GalleryID), "Could not update grid items")
			return
		}
	}
	s.redirect(w, r, s.galleryLink(bl.GalleryID), "Block updated")
}

func (s *Server) handleMoveBlock(w http.ResponseWriter, r *http.Request) {
	bl, ok := s.blockOr404(w, r)
	if !ok {
		return
	}
	up := r.FormValue("dir") == "up"
	if err := s.store.MoveBlock(r.Context(), bl.GalleryID, bl.ID, up); err != nil {
		s.redirect(w, r, s.galleryLink(bl.GalleryID), "Could not reorder block")
		return
	}
	s.redirect(w, r, s.galleryLink(bl.GalleryID), "Blocks reordered")
}

func (s *Server) handleDeleteBlock(w http.ResponseWriter, r *http.Request) {
	bl, ok := s.blockOr404(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteBlock(r.Context(), bl.ID); err != nil {
		s.redirect(w, r, s.galleryLink(bl.GalleryID), "Could not delete block")
		return
	}
	s.redirect(w, r, s.galleryLink(bl.GalleryID), "Block deleted")
}

func (s *Server) blockOr404(w http.ResponseWriter, r *http.Request) (model.Block, bool) {
	id, ok := parseID(w, r)
	if !ok {
		return model.Block{}, false
	}
	bl, err := s.store.Block(r.Context(), id)
	if err != nil {
		http.Error(w, "block not found", http.StatusNotFound)
		return model.Block{}, false
	}
	return bl, true
}

func validBlockType(t model.BlockType) bool {
	switch t {
	case model.BlockHeading, model.BlockText, model.BlockQuote, model.BlockImage, model.BlockGrid:
		return true
	default:
		return false
	}
}
