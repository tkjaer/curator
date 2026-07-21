package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/tkjaer/curator/internal/ingest"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/slug"
)

const maxUpload = 256 << 20 // 256 MiB per upload request

type galleryRow struct {
	ID     int64
	Title  string
	Slug   string
	Status string
	Count  int
	Depth  int
	Indent string
	URL    string
}

type parentOption struct {
	ID    int64
	Label string
}

type dashboardData struct {
	SiteTitle string
	Galleries []galleryRow
	Parents   []parentOption
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	galleries, err := s.store.Galleries(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := s.galleryTree(ctx, galleries)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, r, "dashboard", "Dashboard", s.flash(r), dashboardData{
		SiteTitle: settings["site.title"],
		Galleries: rows,
		Parents:   parentOptions(rows),
	})
}

// galleryTree returns galleries in parent-first order with item counts.
func (s *Server) galleryTree(ctx context.Context, galleries []model.Gallery) ([]galleryRow, error) {
	rows := s.orderRows(galleries)
	for i := range rows {
		n, err := s.store.CountItems(ctx, rows[i].ID)
		if err != nil {
			return nil, err
		}
		rows[i].Count = n
	}
	return rows, nil
}

// orderRows lays galleries out parent-first with a depth and indent, without
// touching the store.
func (s *Server) orderRows(galleries []model.Gallery) []galleryRow {
	childrenOf := map[int64][]model.Gallery{}
	var roots []model.Gallery
	for _, g := range galleries {
		if g.ParentID == nil {
			roots = append(roots, g)
		} else {
			childrenOf[*g.ParentID] = append(childrenOf[*g.ParentID], g)
		}
	}

	var rows []galleryRow
	var walk func(g model.Gallery, depth int)
	walk = func(g model.Gallery, depth int) {
		rows = append(rows, galleryRow{
			ID: g.ID, Title: g.Title, Slug: g.Slug, Status: string(g.Status),
			Depth: depth, Indent: strings.Repeat("— ", depth),
			URL: s.link("galleries", strconv.FormatInt(g.ID, 10)),
		})
		for _, c := range childrenOf[g.ID] {
			walk(c, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return rows
}

// galleryPublicURL builds a gallery's public site URL from its ancestor slugs.
func galleryPublicURL(all []model.Gallery, g model.Gallery, baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	byID := make(map[int64]model.Gallery, len(all))
	for _, x := range all {
		byID[x.ID] = x
	}
	var segs []string
	cur := g
	for {
		segs = append([]string{cur.Slug}, segs...)
		if cur.ParentID == nil {
			break
		}
		p, ok := byID[*cur.ParentID]
		if !ok {
			break
		}
		cur = p
	}
	return baseURL + "/galleries/" + strings.Join(segs, "/") + "/"
}

// descendantSet returns the id plus all of its descendant gallery ids.
func descendantSet(galleries []model.Gallery, id int64) map[int64]bool {
	childrenOf := map[int64][]int64{}
	for _, g := range galleries {
		if g.ParentID != nil {
			childrenOf[*g.ParentID] = append(childrenOf[*g.ParentID], g.ID)
		}
	}
	set := map[int64]bool{id: true}
	stack := []int64{id}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range childrenOf[n] {
			if !set[c] {
				set[c] = true
				stack = append(stack, c)
			}
		}
	}
	return set
}

func parentOptions(rows []galleryRow) []parentOption {
	opts := make([]parentOption, 0, len(rows))
	for _, r := range rows {
		opts = append(opts, parentOption{ID: r.ID, Label: strings.Repeat("— ", r.Depth) + r.Title})
	}
	return opts
}

func (s *Server) handleCreateGallery(w http.ResponseWriter, r *http.Request) {
	title := r.FormValue("title")
	if title == "" {
		s.redirect(w, r, s.link(), "Title is required")
		return
	}
	sl := r.FormValue("slug")
	if sl == "" {
		sl = slug.Make(title)
	}
	gType := model.GalleryGrid
	if r.FormValue("type") == "story" {
		gType = model.GalleryStory
	}

	var parentID *int64
	if p := r.FormValue("parent"); p != "" {
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			parentID = &id
		}
	}

	id, err := s.store.CreateGallery(r.Context(), model.Gallery{
		ParentID: parentID, Slug: sl, Title: title, Type: gType,
		Status: model.GalleryDraft, SortMode: model.SortByDate,
	})
	if err != nil {
		s.redirect(w, r, s.link(), "Could not create gallery: "+err.Error())
		return
	}
	s.redirect(w, r, s.link("galleries", strconv.FormatInt(id, 10)), "Gallery created")
}

func (s *Server) handleMoveGallery(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var parent *int64
	if p := r.FormValue("parent"); p != "" {
		pid, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			s.redirect(w, r, s.galleryLink(id), "Invalid parent")
			return
		}
		parent = &pid
	}
	if err := s.store.MoveGallery(r.Context(), id, parent); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not move: "+err.Error())
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Gallery moved")
}

func (s *Server) handleDeleteGallery(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	g, err := s.store.Gallery(r.Context(), id)
	if err != nil {
		http.Error(w, "gallery not found", http.StatusNotFound)
		return
	}
	if err := s.store.DeleteGallery(r.Context(), id); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not delete gallery")
		return
	}
	dest := s.link()
	if g.ParentID != nil {
		dest = s.galleryLink(*g.ParentID)
	}
	s.redirect(w, r, dest, "Gallery deleted")
}

type galleryData struct {
	Gallery         model.Gallery
	Items           []model.Item
	Statuses        []string
	ItemStatuses    []string
	CoverID         int64
	Protected       bool
	PublicURL       string
	AccessUsers     []accessUserGrant
	Children        []galleryRow
	MoveTargets     []parentOption
	CurrentParentID int64
	IsStory         bool
	Blocks          []blockRow
	BlockTypes      []string
	ItemChoices     []itemChoice
}

type blockRow struct {
	ID        int64
	Type      string
	Content   string
	ItemID    int64
	GridItems map[int64]bool
}

type itemChoice struct {
	ID       int64
	Filename string
}

type accessUserGrant struct {
	ID       int64
	Username string
	Granted  bool
}

func (s *Server) handleGallery(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	g, err := s.store.Gallery(ctx, id)
	if err != nil {
		http.Error(w, "gallery not found", http.StatusNotFound)
		return
	}
	items, err := s.store.ItemsByGallery(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var cover int64
	if g.CoverItemID != nil {
		cover = *g.CoverItemID
	}

	data := galleryData{
		Gallery:      g,
		Items:        items,
		Statuses:     []string{"draft", "unlisted", "published", "protected"},
		ItemStatuses: []string{"draft", "unlisted", "published"},
		CoverID:      cover,
		Protected:    g.Status == model.GalleryProtected,
	}
	if data.Protected {
		users, err := s.store.AccessUsers(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		granted, err := s.store.GalleryAccessUsers(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		grantedIDs := make(map[int64]bool, len(granted))
		for _, u := range granted {
			grantedIDs[u.ID] = true
		}
		for _, u := range users {
			data.AccessUsers = append(data.AccessUsers, accessUserGrant{
				ID: u.ID, Username: u.Username, Granted: grantedIDs[u.ID],
			})
		}
	}

	all, err := s.store.Galleries(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, child := range all {
		if child.ParentID != nil && *child.ParentID == id {
			n, err := s.store.CountItems(ctx, child.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			data.Children = append(data.Children, galleryRow{
				ID: child.ID, Title: child.Title, Slug: child.Slug, Status: string(child.Status),
				Count: n, URL: s.link("galleries", strconv.FormatInt(child.ID, 10)),
			})
		}
	}

	// Move targets: every gallery except this one and its descendants.
	excluded := descendantSet(all, id)
	for _, opt := range parentOptions(s.orderRows(all)) {
		if !excluded[opt.ID] {
			data.MoveTargets = append(data.MoveTargets, opt)
		}
	}
	if g.ParentID != nil {
		data.CurrentParentID = *g.ParentID
	}

	settings, err := s.store.Settings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.PublicURL = galleryPublicURL(all, g, settings["site.base_url"])

	if g.Type == model.GalleryStory {
		data.IsStory = true
		data.BlockTypes = []string{"heading", "text", "quote", "image", "grid"}
		for _, it := range items {
			data.ItemChoices = append(data.ItemChoices, itemChoice{ID: it.ID, Filename: it.Filename})
		}
		blocks, err := s.store.BlocksByGallery(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, bl := range blocks {
			row := blockRow{ID: bl.ID, Type: string(bl.Type), Content: bl.Content}
			if bl.ItemID != nil {
				row.ItemID = *bl.ItemID
			}
			if bl.Type == model.BlockGrid {
				ids, err := s.store.BlockItemIDs(ctx, bl.ID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				row.GridItems = make(map[int64]bool, len(ids))
				for _, iid := range ids {
					row.GridItems[iid] = true
				}
			}
			data.Blocks = append(data.Blocks, row)
		}
	}

	s.render(w, r, "gallery", g.Title, s.flash(r), data)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	g, err := s.store.Gallery(ctx, id)
	if err != nil {
		http.Error(w, "gallery not found", http.StatusNotFound)
		return
	}
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Upload failed: "+err.Error())
		return
	}

	files := r.MultipartForm.File["images"]
	n := 0
	for _, fh := range files {
		if !ingest.IsImage(fh.Filename) {
			continue
		}
		f, err := fh.Open()
		if err != nil {
			s.redirect(w, r, s.galleryLink(id), "Upload failed: "+err.Error())
			return
		}
		err = ingest.ImportUpload(ctx, s.store, s.cfg, id, g.Slug, fh.Filename, f)
		f.Close()
		if err != nil {
			s.redirect(w, r, s.galleryLink(id), "Import failed: "+err.Error())
			return
		}
		n++
	}
	s.redirect(w, r, s.galleryLink(id), pluralize(n, "image")+" uploaded")
}

func (s *Server) handleGalleryStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	status := model.GalleryStatus(r.FormValue("status"))
	if err := s.store.UpdateGalleryStatus(r.Context(), id, status); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not update status")
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Status updated")
}

func (s *Server) handleGalleryEXIF(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	show := r.FormValue("show_exif") == "on"
	if err := s.store.UpdateGalleryShowEXIF(r.Context(), id, show); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not update EXIF setting")
		return
	}
	s.redirect(w, r, s.galleryLink(id), "EXIF setting updated")
}

type settingsData struct {
	Title      string
	BaseURL    string
	Webserver  string
	ServerRoot string
	Facets     []model.FacetConfig
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := s.store.Settings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	facets, err := s.store.FacetConfigs(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "settings", "Settings", s.flash(r), settingsData{
		Title:      settings["site.title"],
		BaseURL:    settings["site.base_url"],
		Webserver:  settings["site.webserver"],
		ServerRoot: settings["site.server_root"],
		Facets:     facets,
	})
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.title", r.FormValue("title")); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.base_url", strings.TrimRight(strings.TrimSpace(r.FormValue("base_url")), "/")); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.webserver", r.FormValue("webserver")); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.server_root", r.FormValue("server_root")); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}

	facets, err := s.store.FacetConfigs(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, f := range facets {
		enabled := r.FormValue("facet_"+f.Namespace) == "on"
		if err := s.store.SetFacetEnabled(ctx, f.Namespace, enabled); err != nil {
			s.redirect(w, r, s.link("settings"), "Could not save facets")
			return
		}
	}
	s.redirect(w, r, s.link("settings"), "Settings saved")
}

func (s *Server) galleryLink(id int64) string {
	return s.link("galleries", strconv.FormatInt(id, 10))
}

func (s *Server) flash(r *http.Request) string {
	return r.URL.Query().Get("msg")
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func pluralize(n int, noun string) string {
	s := strconv.Itoa(n) + " " + noun
	if n != 1 {
		s += "s"
	}
	return s
}
