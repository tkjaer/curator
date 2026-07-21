package admin

import (
	"net/http"
	"strconv"

	"github.com/tkjaer/curator/internal/htpasswd"
)

type accessData struct {
	Users []accessUserRow
}

type accessUserRow struct {
	ID       int64
	Username string
}

func (s *Server) handleAccess(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.AccessUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]accessUserRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, accessUserRow{ID: u.ID, Username: u.Username})
	}
	s.render(w, r, "access", "Access users", s.flash(r), accessData{Users: rows})
}

func (s *Server) handleCreateAccessUser(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	if username == "" || password == "" {
		s.redirect(w, r, s.link("access"), "Username and password are required")
		return
	}
	if _, err := s.store.CreateAccessUser(r.Context(), username, htpasswd.Hash(password)); err != nil {
		s.redirect(w, r, s.link("access"), "Could not create user: "+err.Error())
		return
	}
	s.redirect(w, r, s.link("access"), "User created")
}

func (s *Server) handleDeleteAccessUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteAccessUser(r.Context(), id); err != nil {
		s.redirect(w, r, s.link("access"), "Could not delete user")
		return
	}
	s.redirect(w, r, s.link("access"), "User deleted")
}

func (s *Server) handleGalleryAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not update access")
		return
	}

	users, err := s.store.AccessUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var granted []int64
	for _, u := range users {
		if r.FormValue("user_"+strconv.FormatInt(u.ID, 10)) == "on" {
			granted = append(granted, u.ID)
		}
	}
	if err := s.store.SetGalleryAccess(r.Context(), id, granted); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not update access")
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Access updated")
}
