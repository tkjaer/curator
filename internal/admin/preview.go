package admin

import (
	"net/http"
	"path/filepath"
)

func (s *Server) handleStoryPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if s.storyPreview == nil {
		http.Error(w, "Story preview is not available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.storyPreview(r.Context(), id, s.link("galleries", r.PathValue("id"), "preview"), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleStoryPreviewAsset(w http.ResponseWriter, r *http.Request) {
	if s.previewAssets == nil {
		http.NotFound(w, r)
		return
	}
	assets, err := s.previewAssets(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.ServeFileFS(w, r, assets, r.PathValue("file"))
}

func (s *Server) handleStoryPreviewImage(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(s.cfg.OutputDir, "_curator", "img", filepath.Base(r.PathValue("file"))))
}
