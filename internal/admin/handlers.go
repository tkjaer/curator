package admin

import (
	"context"
	"io"
	"maps"
	"math"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tkjaer/curator/internal/ingest"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/publishapi"
	"github.com/tkjaer/curator/internal/slug"
	"github.com/tkjaer/curator/internal/store"
)

const maxUpload = 256 << 20 // 256 MiB per upload request

type galleryRow struct {
	ID             int64
	ParentID       *int64
	Title          string
	Slug           string
	Status         string
	Count          int
	TotalCount     int
	Depth          int
	HasChildren    bool
	CanMoveEarlier bool
	CanMoveLater   bool
	URL            string
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
	rowByID := make(map[int64]int, len(rows))
	for i := range rows {
		n, err := s.store.CountItems(ctx, rows[i].ID)
		if err != nil {
			return nil, err
		}
		rows[i].Count = n
		rows[i].TotalCount = n
		rowByID[rows[i].ID] = i
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].ParentID == nil {
			continue
		}
		if parent, ok := rowByID[*rows[i].ParentID]; ok {
			rows[parent].TotalCount += rows[i].TotalCount
		}
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
	var walk func(galleries []model.Gallery, depth int)
	walk = func(galleries []model.Gallery, depth int) {
		for index, g := range galleries {
			rows = append(rows, galleryRow{
				ID: g.ID, ParentID: g.ParentID, Title: g.Title, Slug: g.Slug,
				Status: string(g.Status), Depth: depth, HasChildren: len(childrenOf[g.ID]) > 0,
				CanMoveEarlier: index > 0, CanMoveLater: index < len(galleries)-1,
				URL: s.link("galleries", strconv.FormatInt(g.ID, 10)),
			})
			walk(childrenOf[g.ID], depth+1)
		}
	}
	walk(roots, 0)
	return rows
}

func (s *Server) handleGalleryPosition(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	direction := r.FormValue("direction")
	if direction != "earlier" && direction != "later" {
		s.redirect(w, r, s.path("/"), "Invalid gallery position")
		return
	}
	if err := s.store.MoveGalleryOrder(r.Context(), id, direction == "earlier"); err != nil {
		s.redirect(w, r, s.path("/"), "Could not reorder gallery")
		return
	}
	s.redirect(w, r, s.path("/"), "Gallery order updated")
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
	return baseURL + "/" + strings.Join(segs, "/") + "/"
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
	defaultStatus, err := s.store.GalleryDefaults(r.Context())
	if err != nil {
		s.redirect(w, r, s.link(), "Could not load gallery defaults")
		return
	}
	id, err := s.store.CreateGallery(r.Context(), model.Gallery{
		ParentID: parentID, Slug: sl, Title: title, Type: gType,
		Status: defaultStatus, SortMode: model.SortDefault,
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

func (s *Server) handleGalleryTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	managed, err := s.store.IsExternalGallery(r.Context(), "lightroom", id)
	if err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not check gallery ownership")
		return
	}
	if managed {
		s.redirect(w, r, s.galleryLink(id), "Could not update title: managed by Lightroom")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if err := s.store.UpdateGalleryTitle(r.Context(), id, title); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not update title: "+err.Error())
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Title updated")
}

func (s *Server) handleGallerySlug(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	gallerySlug := slug.Make(r.FormValue("slug"))
	if err := s.store.UpdateGallerySlug(r.Context(), id, gallerySlug); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not change URL: "+err.Error())
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Gallery URL changed")
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
	Gallery               model.Gallery
	Breadcrumbs           []galleryRow
	Items                 []model.Item
	CameraSuggestions     []store.CameraSuggestion
	LensSuggestions       []store.LensSuggestion
	TagSuggestions        []model.Tag
	ItemTags              map[int64]string
	ItemImportedTags      map[int64]string
	ItemLightroomManaged  map[int64]bool
	Statuses              []string
	ItemStatuses          []string
	CoverID               int64
	Protected             bool
	LightroomManaged      bool
	PublicURL             string
	AccessUsers           []accessUserGrant
	Children              []galleryRow
	MoveTargets           []parentOption
	CurrentParentID       int64
	IsStory               bool
	StoryPreviewAvailable bool
	Blocks                []blockRow
	BlockTypes            []string
	ItemChoices           []itemChoice
	CustomOrder           bool
	AutomaticOrder        string
	AutomaticDirection    string
	PresentationDefaults  model.GalleryPresentationDefaults
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

type tagReviewRow struct {
	store.TagUsage
	Public bool
	Source string
}

type tagsData struct {
	Tags []tagReviewRow
}

type tagGalleryGroup struct {
	ID    int64
	Title string
	Items []store.TagUsageItem
}

type tagData struct {
	Tag    tagReviewRow
	Groups []tagGalleryGroup
}

func (s *Server) tagReviewRows(ctx context.Context) ([]tagReviewRow, bool, error) {
	usages, err := s.store.TagUsages(ctx)
	if err != nil {
		return nil, false, err
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return nil, false, err
	}
	facets, err := s.store.FacetConfigs(ctx)
	if err != nil {
		return nil, false, err
	}
	browsePages := false
	for _, facet := range facets {
		if facet.Namespace == "tag" {
			browsePages = facet.Enabled
			break
		}
	}
	selected := make(map[string]bool)
	for _, value := range strings.Split(settings["metadata.tag_selection"], "\n") {
		if value = strings.TrimSpace(value); value != "" {
			selected[strings.ToLower(value)] = true
		}
	}
	rows := make([]tagReviewRow, 0, len(usages))
	for _, usage := range usages {
		visible := true
		switch settings["metadata.tag_visibility"] {
		case "hide_all":
			visible = false
		case "show_selected":
			visible = selected[strings.ToLower(usage.Value)]
		case "hide_selected":
			visible = !selected[strings.ToLower(usage.Value)]
		}
		var sources []string
		if usage.ManualCount > 0 {
			sources = append(sources, "Curator")
		}
		if usage.MetadataCount > 0 {
			sources = append(sources, "Metadata")
		}
		if usage.LightroomCount > 0 {
			sources = append(sources, "Lightroom")
		}
		rows = append(rows, tagReviewRow{
			TagUsage: usage,
			Public:   browsePages && visible && usage.PublishedCount > 0,
			Source:   strings.Join(sources, ", "),
		})
	}
	return rows, browsePages, nil
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	rows, _, err := s.tagReviewRows(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "tags", "Tags", "", tagsData{Tags: rows})
}

func (s *Server) handleTag(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	rows, _, err := s.tagReviewRows(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var selected tagReviewRow
	for _, row := range rows {
		if row.ID == id {
			selected = row
			break
		}
	}
	if selected.ID == 0 {
		http.Redirect(w, r, s.link("tags"), http.StatusSeeOther)
		return
	}
	items, err := s.store.TagUsageItems(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var groups []tagGalleryGroup
	for _, item := range items {
		if len(groups) == 0 || groups[len(groups)-1].ID != item.GalleryID {
			groups = append(groups, tagGalleryGroup{ID: item.GalleryID, Title: item.GalleryTitle})
		}
		groups[len(groups)-1].Items = append(groups[len(groups)-1].Items, item)
	}
	s.render(w, r, "tag", selected.Value, "", tagData{Tag: selected, Groups: groups})
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
	cameraSuggestions, err := s.store.CameraSuggestions(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lensSuggestions, err := s.store.LensSuggestions(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tagSuggestions, err := s.store.UserTags(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	itemTagValues, err := s.store.GalleryItemUserTags(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	itemManualTagValues, err := s.store.GalleryItemManualTags(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	itemImportedTagValues, err := s.store.GalleryItemImportedTags(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	itemLightroomManaged, err := s.store.GalleryExternalItemIDs(ctx, "lightroom", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	itemTags := make(map[int64]string, len(itemTagValues))
	itemImportedTags := make(map[int64]string, len(itemImportedTagValues))
	for _, item := range items {
		if itemLightroomManaged[item.ID] {
			itemTags[item.ID] = strings.Join(itemManualTagValues[item.ID], ", ")
			itemImportedTags[item.ID] = strings.Join(itemImportedTagValues[item.ID], ", ")
		} else {
			itemTags[item.ID] = strings.Join(itemTagValues[item.ID], ", ")
		}
	}
	var cover int64
	if g.CoverItemID != nil {
		cover = *g.CoverItemID
	}

	data := galleryData{
		Gallery:               g,
		Items:                 items,
		CameraSuggestions:     cameraSuggestions,
		LensSuggestions:       lensSuggestions,
		TagSuggestions:        tagSuggestions,
		ItemTags:              itemTags,
		ItemImportedTags:      itemImportedTags,
		ItemLightroomManaged:  itemLightroomManaged,
		Statuses:              []string{"draft", "unlisted", "published", "protected"},
		ItemStatuses:          []string{"draft", "unlisted", "published"},
		CoverID:               cover,
		Protected:             g.Status == model.GalleryProtected,
		AutomaticOrder:        "Date taken",
		AutomaticDirection:    "Ascending",
		StoryPreviewAvailable: s.storyPreview != nil,
	}
	data.LightroomManaged, err = s.store.IsExternalGallery(ctx, "lightroom", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.PresentationDefaults, err = s.store.GalleryPresentationDefaults(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	effectiveSortMode, err := s.store.EffectiveGallerySortMode(ctx, g.SortMode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if effectiveSortMode == model.SortByDateAdded {
		data.AutomaticOrder = "Date added"
	} else if effectiveSortMode == model.SortByFilename {
		data.AutomaticOrder = "Alphabetical"
	}
	effectiveDirection, err := s.store.EffectiveGallerySortDirection(ctx, g.SortDirection)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if effectiveDirection == model.SortDescending {
		data.AutomaticDirection = "Descending"
	}
	for _, item := range items {
		if item.SortOrder != 0 {
			data.CustomOrder = true
			break
		}
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
	byID := make(map[int64]model.Gallery, len(all))
	for _, gallery := range all {
		byID[gallery.ID] = gallery
	}
	for parentID := g.ParentID; parentID != nil; {
		parent, ok := byID[*parentID]
		if !ok {
			break
		}
		data.Breadcrumbs = append([]galleryRow{{
			ID: parent.ID, Title: parent.Title,
			URL: s.link("galleries", strconv.FormatInt(parent.ID, 10)),
		}}, data.Breadcrumbs...)
		parentID = parent.ParentID
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
	sidecars := map[string]int{}
	for i, fh := range files {
		if strings.EqualFold(filepath.Ext(fh.Filename), ".xmp") {
			sidecars[uploadStem(fh.Filename)] = i
		}
	}
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
		var sidecar io.ReadCloser
		if i, ok := sidecars[uploadStem(fh.Filename)]; ok {
			sidecar, err = files[i].Open()
			if err != nil {
				f.Close()
				s.redirect(w, r, s.galleryLink(id), "Upload failed: "+err.Error())
				return
			}
		}
		err = ingest.ImportUploadWithSidecar(ctx, s.store, s.cfg, id, g.Slug, fh.Filename, f, sidecar)
		f.Close()
		if sidecar != nil {
			sidecar.Close()
		}
		if err != nil {
			s.redirect(w, r, s.galleryLink(id), "Import failed: "+err.Error())
			return
		}
		n++
	}
	s.redirect(w, r, s.galleryLink(id), pluralize(n, "image")+" uploaded")
}

func uploadStem(name string) string {
	name = filepath.Base(name)
	if strings.EqualFold(filepath.Ext(name), ".xmp") {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	if ingest.IsImage(name) {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	return strings.ToLower(name)
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

func (s *Server) handleGalleryPresentation(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := s.store.UpdateGalleryPresentation(r.Context(), id,
		model.ParseVisibility(r.FormValue("show_exif")),
		model.ParseVisibility(r.FormValue("show_title")),
		model.ParseVisibility(r.FormValue("show_description"))); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not update metadata display")
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Metadata display updated")
}

func (s *Server) handleGalleryOptionsReset(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := s.store.ResetGalleryOptions(r.Context(), id); err != nil {
		s.redirect(w, r, s.galleryLink(id), "Could not reset gallery options")
		return
	}
	s.redirect(w, r, s.galleryLink(id), "Gallery options reset")
}

type settingsData struct {
	Title                  string
	BaseURL                string
	CopyrightHolder        string
	CopyrightYear          string
	CurrentYear            int
	Theme                  string
	Themes                 []string
	DefaultOrder           string
	DefaultDirection       string
	DefaultPublished       bool
	DefaultShowEXIF        bool
	DefaultShowTitle       bool
	DefaultShowDescription bool
}

type lensMappingRow struct {
	Camera            string
	Lens              string
	Suggestion        string
	Evidence          string
	XMPName           string
	XMPFallbackActive bool
	CanEnableFallback bool
}

type lensNameMappingRow struct {
	Existing  string
	Canonical string
}

type metadataSettingsData struct {
	UseXMPFallback    bool
	Mappings          []lensMappingRow
	LensNameMappings  []lensNameMappingRow
	LensSuggestions   []store.LensSuggestion
	XMPProfiles       []xmpProfileRow
	Facets            []model.FacetConfig
	TagSuggestions    []model.Tag
	SelectedTags      map[string]bool
	TagVisibility     string
	TagBrowseEnabled  bool
	PaginationEnabled bool
	PageSize          int
}

type publishingSettingsData struct {
	BaseURL                string
	FeedEnabled            bool
	Webserver              string
	ServerRoot             string
	RsyncEnabled           bool
	RsyncTarget            string
	RsyncDelete            bool
	PublishTokenConfigured bool
	GeneratedPublishToken  string
	RsyncPreviewRan        bool
	RsyncPreview           string
	RsyncPreviewError      string
}

type xmpProfileRow struct {
	Name         string
	Cameras      string
	Count        int
	MappedCount  int
	SidecarCount int
	Status       string
}

// themeOr returns the configured theme name, defaulting to "default" when unset.
func themeOr(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

// validTheme reports whether name is one of the available themes.
func (s *Server) validTheme(name string) bool {
	for _, t := range s.themes {
		if t == name {
			return true
		}
	}
	return false
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := s.store.Settings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "settings", "Settings", s.flash(r), settingsData{
		Title:                  settings["site.title"],
		BaseURL:                settings["site.base_url"],
		CopyrightHolder:        settings["site.copyright_holder"],
		CopyrightYear:          settings["site.copyright_start_year"],
		CurrentYear:            time.Now().Year(),
		Theme:                  themeOr(settings["site.theme"]),
		Themes:                 s.themes,
		DefaultPublished:       settings["site.default_gallery_published"] == "true",
		DefaultShowEXIF:        settings["site.default_gallery_show_exif"] == "true",
		DefaultShowTitle:       settings["site.default_gallery_show_title"] != "false",
		DefaultShowDescription: settings["site.default_gallery_show_description"] != "false",
		DefaultOrder: func() string {
			mode := model.SortMode(settings["site.default_gallery_order"])
			if mode == model.SortByDateAdded || mode == model.SortByFilename {
				return string(mode)
			}
			return string(model.SortByDate)
		}(),
		DefaultDirection: func() string {
			if settings["site.default_gallery_sort_direction"] == string(model.SortDescending) {
				return string(model.SortDescending)
			}
			return string(model.SortAscending)
		}(),
	})
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	title := r.FormValue("title")
	baseURL := strings.TrimRight(strings.TrimSpace(r.FormValue("base_url")), "/")
	copyrightHolder := strings.TrimSpace(r.FormValue("copyright_holder"))
	copyrightYear := strings.TrimSpace(r.FormValue("copyright_start_year"))
	if copyrightHolder != "" && copyrightYear == "" {
		copyrightYear = strconv.Itoa(time.Now().Year())
	}
	if copyrightYear != "" {
		year, err := strconv.Atoi(copyrightYear)
		if err != nil || year <= 0 || year > time.Now().Year() {
			s.redirect(w, r, s.link("settings"), "Copyright start year must be a valid year")
			return
		}
	}
	theme := themeOr(settings["site.theme"])
	if requestedTheme := r.FormValue("theme"); s.validTheme(requestedTheme) {
		theme = requestedTheme
	}
	buildNeeded := settings["site.title"] != title ||
		settings["site.base_url"] != baseURL ||
		settings["site.copyright_holder"] != copyrightHolder ||
		settings["site.copyright_start_year"] != copyrightYear ||
		themeOr(settings["site.theme"]) != theme

	if err := s.store.SetSetting(ctx, "site.title", title); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.base_url", baseURL); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.copyright_holder", copyrightHolder); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.copyright_start_year", copyrightYear); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if s.validTheme(theme) {
		if err := s.store.SetSetting(ctx, "site.theme", theme); err != nil {
			s.redirect(w, r, s.link("settings"), "Could not save settings")
			return
		}
	}
	defaultOrder := model.SortMode(r.FormValue("default_gallery_order"))
	if defaultOrder != model.SortByDateAdded && defaultOrder != model.SortByFilename {
		defaultOrder = model.SortByDate
	}
	defaultOrderChanged := settings["site.default_gallery_order"] != string(defaultOrder)
	if defaultOrderChanged {
		buildNeeded = true
	}
	if err := s.store.SetSetting(ctx, "site.default_gallery_order", string(defaultOrder)); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	defaultDirection := model.SortDirection(r.FormValue("default_gallery_sort_direction"))
	if defaultDirection != model.SortDescending {
		defaultDirection = model.SortAscending
	}
	if settings["site.default_gallery_sort_direction"] != string(defaultDirection) {
		buildNeeded = true
	}
	if err := s.store.SetSetting(ctx, "site.default_gallery_sort_direction", string(defaultDirection)); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.default_gallery_published", strconv.FormatBool(r.FormValue("default_gallery_published") == "on")); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	defaultShowEXIF := strconv.FormatBool(r.FormValue("default_gallery_show_exif") == "on")
	defaultShowTitle := strconv.FormatBool(r.FormValue("default_gallery_show_title") == "on")
	defaultShowDescription := strconv.FormatBool(r.FormValue("default_gallery_show_description") == "on")
	if settings["site.default_gallery_show_exif"] != defaultShowEXIF ||
		settings["site.default_gallery_show_title"] != defaultShowTitle ||
		settings["site.default_gallery_show_description"] != defaultShowDescription {
		buildNeeded = true
	}
	if err := s.store.SetSetting(ctx, "site.default_gallery_show_exif", defaultShowEXIF); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.default_gallery_show_title", defaultShowTitle); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	if err := s.store.SetSetting(ctx, "site.default_gallery_show_description", defaultShowDescription); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not save settings")
		return
	}
	message := "Site settings saved"
	if buildNeeded {
		message += "; build site to publish changes"
	}
	s.redirect(w, r, s.link("settings"), message)
}

func (s *Server) handleResetGalleryPresentation(w http.ResponseWriter, r *http.Request) {
	field := r.FormValue("field")
	if field == "" {
		field = "all"
	}
	if err := s.store.ResetGalleryPresentationOverrides(r.Context(), field); err != nil {
		s.redirect(w, r, s.link("settings"), "Could not reset gallery metadata display")
		return
	}
	label := map[string]string{"title": "Title", "description": "Description", "exif": "EXIF", "all": "All photo details"}[field]
	s.redirect(w, r, s.link("settings"), label+" overrides reset; build site to publish changes")
}

func (s *Server) handleMetadataSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := s.store.Settings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mappings, err := ingest.ParseLensMappings(settings["metadata.lens_mappings"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lensNameMappings, err := ingest.ParseLensNameMappings(settings["metadata.lens_name_mappings"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lensSuggestions, err := s.store.LensSuggestions(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	clues, err := s.store.CameraLensClues(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	profileUsages, err := s.store.XMPProfileUsages(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	facets, err := s.store.FacetConfigs(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tagSuggestions, err := s.store.UserTags(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	selectedTags := make(map[string]bool)
	for _, value := range strings.Split(settings["metadata.tag_selection"], "\n") {
		if value = strings.TrimSpace(value); value != "" {
			selectedTags[value] = true
		}
	}
	tagVisibility := settings["metadata.tag_visibility"]
	if tagVisibility != "hide_all" && tagVisibility != "show_selected" && tagVisibility != "hide_selected" {
		tagVisibility = "show_all"
	}
	var metadataFacets []model.FacetConfig
	tagBrowseEnabled := false
	for _, facet := range facets {
		if facet.Namespace == "tag" {
			tagBrowseEnabled = facet.Enabled
			continue
		}
		metadataFacets = append(metadataFacets, facet)
	}

	useXMPFallback := settings["metadata.use_lightroom_lens_profile"] == "true"
	suggestions := cameraLensSuggestions(clues, useXMPFallback)
	rows := make([]lensMappingRow, 0, len(mappings)+len(suggestions))
	for camera, lens := range mappings {
		suggestion := suggestions[camera]
		rows = append(rows, lensMappingRow{
			Camera: camera, Lens: lens, Evidence: suggestion.Evidence,
			XMPName: suggestion.XMPName, XMPFallbackActive: suggestion.XMPFallbackActive,
			CanEnableFallback: suggestion.CanEnableFallback,
		})
	}
	slices.SortFunc(rows, func(a, b lensMappingRow) int {
		return strings.Compare(strings.ToLower(a.Camera), strings.ToLower(b.Camera))
	})
	for _, camera := range slices.Sorted(maps.Keys(suggestions)) {
		if _, exists := mappings[camera]; !exists {
			suggestion := suggestions[camera]
			rows = append(rows, lensMappingRow{
				Camera: camera, Suggestion: suggestion.Lens, Evidence: suggestion.Evidence,
				XMPName: suggestion.XMPName, XMPFallbackActive: suggestion.XMPFallbackActive,
				CanEnableFallback: suggestion.CanEnableFallback,
			})
		}
	}
	if len(rows) == 0 {
		rows = append(rows, lensMappingRow{})
	}
	nameRows := make([]lensNameMappingRow, 0, len(lensNameMappings))
	for _, existing := range slices.Sorted(maps.Keys(lensNameMappings)) {
		nameRows = append(nameRows, lensNameMappingRow{Existing: existing, Canonical: lensNameMappings[existing]})
	}

	pageSize, err := strconv.Atoi(settings["metadata.facet_page_size"])
	if err != nil || pageSize < 1 {
		pageSize = 100
	}
	s.render(w, r, "metadata-settings", "Metadata settings", s.flash(r), metadataSettingsData{
		UseXMPFallback:    useXMPFallback,
		Mappings:          rows,
		LensNameMappings:  nameRows,
		LensSuggestions:   lensSuggestions,
		XMPProfiles:       xmpProfileRows(profileUsages, mappings, useXMPFallback),
		Facets:            metadataFacets,
		TagSuggestions:    tagSuggestions,
		SelectedTags:      selectedTags,
		TagVisibility:     tagVisibility,
		TagBrowseEnabled:  tagBrowseEnabled,
		PaginationEnabled: settings["metadata.facet_pagination_enabled"] != "false",
		PageSize:          pageSize,
	})
}

type cameraLensSuggestion struct {
	Lens              string
	Evidence          string
	XMPName           string
	XMPFallbackActive bool
	CanEnableFallback bool
}

func cameraLensSuggestions(clues []store.CameraLensClue, useXMPFallback bool) map[string]cameraLensSuggestion {
	type evidence struct {
		count     int
		focals    map[string]bool
		apertures map[string]bool
		profiles  map[string]bool
	}
	byCamera := map[string]*evidence{}
	for _, clue := range clues {
		e := byCamera[clue.Camera]
		if e == nil {
			e = &evidence{focals: map[string]bool{}, apertures: map[string]bool{}, profiles: map[string]bool{}}
			byCamera[clue.Camera] = e
		}
		e.count += clue.Count
		if clue.Focal != "" {
			e.focals[clue.Focal] = true
		}
		if aperture := formatMaxAperture(clue.MaxApertureAPEX); aperture != "" {
			e.apertures[aperture] = true
		}
		if clue.XMPProfile != "" {
			e.profiles[clue.XMPProfile] = true
		}
	}

	out := make(map[string]cameraLensSuggestion, len(byCamera))
	for camera, e := range byCamera {
		parts := []string{pluralize(e.count, "photo")}
		if len(e.focals) == 1 {
			parts = append(parts, firstKey(e.focals))
		}
		if len(e.apertures) == 1 {
			parts = append(parts, "max "+firstKey(e.apertures))
		}
		if len(e.profiles) == 1 {
			parts = append(parts, "XMP lens metadata found")
		} else if len(e.profiles) > 1 {
			parts = append(parts, pluralize(len(e.profiles), "different XMP lens name")+"; one mapping would affect all photos")
		}

		var lens string
		if len(e.profiles) == 0 && len(e.focals) == 1 && len(e.apertures) == 1 {
			lens = camera + " " + strings.ReplaceAll(firstKey(e.focals), " ", "") + " " + firstKey(e.apertures)
			parts = append(parts, "one mapping affects every photo from this camera")
		}
		suggestion := cameraLensSuggestion{
			Lens: lens, Evidence: strings.Join(parts, " · "),
			XMPFallbackActive: len(e.profiles) == 1 && useXMPFallback,
			CanEnableFallback: len(e.profiles) == 1 && !useXMPFallback,
		}
		if len(e.profiles) == 1 {
			suggestion.XMPName = firstKey(e.profiles)
		}
		out[camera] = suggestion
	}
	return out
}

func firstKey(values map[string]bool) string {
	for value := range values {
		return value
	}
	return ""
}

func formatMaxAperture(raw string) string {
	numerator, denominator, ok := strings.Cut(raw, "/")
	if !ok {
		return ""
	}
	n, err := strconv.ParseFloat(numerator, 64)
	if err != nil {
		return ""
	}
	d, err := strconv.ParseFloat(denominator, 64)
	if err != nil || d == 0 {
		return ""
	}
	fNumber := math.Pow(2, (n/d)/2)
	return "f/" + strings.TrimSuffix(strconv.FormatFloat(fNumber, 'f', 1, 64), ".0")
}

func xmpProfileRows(usages []store.XMPProfileUsage, mappings map[string]string, enabled bool) []xmpProfileRow {
	var rows []xmpProfileRow
	for _, usage := range usages {
		if len(rows) == 0 || rows[len(rows)-1].Name != usage.Profile {
			rows = append(rows, xmpProfileRow{Name: usage.Profile})
		}
		row := &rows[len(rows)-1]
		if usage.Camera != "" {
			if row.Cameras != "" {
				row.Cameras += ", "
			}
			row.Cameras += usage.Camera
		}
		row.Count += usage.Count
		row.SidecarCount += usage.SidecarCount
		if mappings[usage.Camera] != "" {
			row.MappedCount += usage.Count - usage.SidecarCount
		}
	}
	for i := range rows {
		switch {
		case !enabled:
			rows[i].Status = "Disabled"
		case rows[i].SidecarCount == rows[i].Count:
			rows[i].Status = "Overridden by sidecar"
		case rows[i].MappedCount == rows[i].Count:
			rows[i].Status = "Overridden by mapping"
		case rows[i].SidecarCount+rows[i].MappedCount == rows[i].Count:
			rows[i].Status = "Overridden"
		case rows[i].SidecarCount+rows[i].MappedCount > 0:
			rows[i].Status = "Partially overridden"
		default:
			rows[i].Status = "Used"
		}
	}
	return rows
}

func (s *Server) handleSaveMetadataSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save metadata settings")
		return
	}
	cameras, lenses := r.Form["mapping_camera"], r.Form["mapping_lens"]
	count := max(len(cameras), len(lenses))
	lines := make([]string, 0, count)
	for i := 0; i < count; i++ {
		var camera, lens string
		if i < len(cameras) {
			camera = strings.TrimSpace(cameras[i])
		}
		if i < len(lenses) {
			lens = strings.TrimSpace(lenses[i])
		}
		if lens == "" {
			continue
		}
		if camera == "" || strings.Contains(camera, "=") {
			s.redirect(w, r, s.link("settings", "metadata"), "Each mapping needs a camera and lens")
			return
		}
		lines = append(lines, camera+" = "+lens)
	}
	lensMappings := strings.Join(lines, "\n")
	if _, err := ingest.ParseLensMappings(lensMappings); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), err.Error())
		return
	}
	existingNames, canonicalNames := r.Form["lens_name_existing"], r.Form["lens_name_canonical"]
	nameCount := max(len(existingNames), len(canonicalNames))
	nameLines := make([]string, 0, nameCount)
	for i := 0; i < nameCount; i++ {
		var existing, canonical string
		if i < len(existingNames) {
			existing = strings.TrimSpace(existingNames[i])
		}
		if i < len(canonicalNames) {
			canonical = strings.TrimSpace(canonicalNames[i])
		}
		if existing == "" && canonical == "" {
			continue
		}
		if existing == "" || canonical == "" || strings.Contains(existing, "=") {
			s.redirect(w, r, s.link("settings", "metadata"), "Each lens-name mapping needs an existing and canonical name")
			return
		}
		nameLines = append(nameLines, existing+" = "+canonical)
	}
	lensNameMappings := strings.Join(nameLines, "\n")
	if _, err := ingest.ParseLensNameMappings(lensNameMappings); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), err.Error())
		return
	}

	ctx := r.Context()
	settings, err := s.store.Settings(ctx)
	if err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save metadata settings")
		return
	}
	pageSizeValue := r.FormValue("facet_page_size")
	if pageSizeValue == "" {
		pageSizeValue = settings["metadata.facet_page_size"]
		if pageSizeValue == "" {
			pageSizeValue = "100"
		}
	}
	pageSize, err := strconv.Atoi(pageSizeValue)
	if err != nil || pageSize < 1 || pageSize > 1000 {
		s.redirect(w, r, s.link("settings", "metadata"), "Photos per browse page must be between 1 and 1000")
		return
	}
	useXMPFallback := "false"
	if r.FormValue("use_lightroom_lens_profile") == "on" {
		useXMPFallback = "true"
	}
	if err := s.store.SetSetting(ctx, "metadata.use_lightroom_lens_profile", useXMPFallback); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save metadata settings")
		return
	}
	if err := s.store.SetSetting(ctx, "metadata.lens_mappings", lensMappings); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save metadata settings")
		return
	}
	if err := s.store.SetSetting(ctx, "metadata.lens_name_mappings", lensNameMappings); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save metadata settings")
		return
	}
	paginationEnabled := "false"
	if r.FormValue("facet_pagination_enabled") == "on" {
		paginationEnabled = "true"
	}
	if err := s.store.SetSetting(ctx, "metadata.facet_pagination_enabled", paginationEnabled); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save metadata settings")
		return
	}
	if err := s.store.SetSetting(ctx, "metadata.facet_page_size", strconv.Itoa(pageSize)); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save metadata settings")
		return
	}
	tagVisibility := r.FormValue("tag_visibility")
	if tagVisibility == "" {
		tagVisibility = settings["metadata.tag_visibility"]
		if tagVisibility == "" {
			tagVisibility = "show_all"
		}
	}
	if tagVisibility != "show_all" && tagVisibility != "hide_all" && tagVisibility != "show_selected" && tagVisibility != "hide_selected" {
		s.redirect(w, r, s.link("settings", "metadata"), "Choose a valid tag visibility mode")
		return
	}
	seenTags := make(map[string]bool)
	selectedTags := make([]string, 0, len(r.Form["selected_tag"]))
	for _, value := range r.Form["selected_tag"] {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seenTags[key] {
			continue
		}
		seenTags[key] = true
		selectedTags = append(selectedTags, value)
	}
	if err := s.store.SetSetting(ctx, "metadata.tag_visibility", tagVisibility); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save tag visibility")
		return
	}
	if err := s.store.SetSetting(ctx, "metadata.tag_selection", strings.Join(selectedTags, "\n")); err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save tag visibility")
		return
	}
	facets, err := s.store.FacetConfigs(ctx)
	if err != nil {
		s.redirect(w, r, s.link("settings", "metadata"), "Could not save metadata settings")
		return
	}
	for _, facet := range facets {
		enabled := r.FormValue("facet_"+facet.Namespace) == "on"
		if facet.Namespace == "tag" {
			enabled = r.FormValue("tag_browse_enabled") == "on"
		}
		if err := s.store.SetFacetEnabled(ctx, facet.Namespace, enabled); err != nil {
			s.redirect(w, r, s.link("settings", "metadata"), "Could not save public browse pages")
			return
		}
	}
	s.redirect(w, r, s.link("settings", "metadata"), "Metadata settings saved; build site to publish changes")
}

func (s *Server) handlePublishingSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "publishing-settings", "Publishing settings", s.flash(r), newPublishingSettingsData(settings))
}

func newPublishingSettingsData(settings map[string]string) publishingSettingsData {
	return publishingSettingsData{
		BaseURL:                settings["site.base_url"],
		FeedEnabled:            settings["site.feed_enabled"] == "true",
		Webserver:              settings["site.webserver"],
		ServerRoot:             settings["site.server_root"],
		RsyncEnabled:           settings["publish.rsync_enabled"] == "true",
		RsyncTarget:            settings["publish.rsync_target"],
		RsyncDelete:            settings["publish.rsync_delete"] == "true",
		PublishTokenConfigured: settings["publish.api_token_hash"] != "",
	}
}

func (s *Server) handlePreviewRsync(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := newPublishingSettingsData(settings)
	data.RsyncPreviewRan = true
	target := strings.TrimSpace(settings["publish.rsync_target"])
	if target == "" {
		data.RsyncPreviewError = "Save an rsync destination before previewing remote changes."
	} else {
		output, previewErr := s.previewDeploy(r.Context(), target, true)
		data.RsyncPreview = strings.TrimSpace(output)
		if previewErr != nil {
			data.RsyncPreviewError = previewErr.Error()
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, "publishing-settings", "Publishing settings", "", data)
}

func (s *Server) handleCreatePublishToken(w http.ResponseWriter, r *http.Request) {
	token, err := publishapi.GenerateToken()
	if err != nil {
		s.redirect(w, r, s.link("settings", "publishing"), "Could not generate publishing token")
		return
	}
	hash := publishapi.TokenHash(token)
	if err := s.store.SetSetting(r.Context(), "publish.api_token_hash", hash); err != nil {
		s.redirect(w, r, s.link("settings", "publishing"), "Could not save publishing token")
		return
	}
	s.publishTokenHash = hash
	s.publishAPI.SetTokenHash(hash)

	settings, err := s.store.Settings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	data := newPublishingSettingsData(settings)
	data.PublishTokenConfigured = true
	data.GeneratedPublishToken = token
	s.render(w, r, "publishing-settings", "Publishing settings", "Publishing token generated; copy it now", data)
}

func (s *Server) handleSavePublishingSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirect(w, r, s.link("settings", "publishing"), "Could not save publishing settings")
		return
	}
	ctx := r.Context()
	settings, err := s.store.Settings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	feedEnabled := settings["site.base_url"] != "" && r.FormValue("feed_enabled") == "on"
	webserver := r.FormValue("webserver")
	if webserver != "apache" {
		webserver = "nginx"
	}
	serverRoot := strings.TrimSpace(r.FormValue("server_root"))
	rsyncEnabled := r.FormValue("rsync_enabled") == "on"
	rsyncTarget := strings.TrimSpace(r.FormValue("rsync_target"))
	rsyncDelete := r.FormValue("rsync_delete") == "on"
	if rsyncEnabled && rsyncTarget == "" {
		s.redirect(w, r, s.link("settings", "publishing"), "Rsync destination is required when deployment is enabled")
		return
	}
	buildNeeded := (settings["site.feed_enabled"] == "true") != feedEnabled ||
		settings["site.webserver"] != webserver || settings["site.server_root"] != serverRoot

	feedValue := "false"
	if feedEnabled {
		feedValue = "true"
	}
	for key, value := range map[string]string{
		"site.feed_enabled":     feedValue,
		"site.webserver":        webserver,
		"site.server_root":      serverRoot,
		"publish.rsync_enabled": strconv.FormatBool(rsyncEnabled),
		"publish.rsync_target":  rsyncTarget,
		"publish.rsync_delete":  strconv.FormatBool(rsyncDelete),
	} {
		if err := s.store.SetSetting(ctx, key, value); err != nil {
			s.redirect(w, r, s.link("settings", "publishing"), "Could not save publishing settings")
			return
		}
	}
	message := "Publishing settings saved"
	if buildNeeded {
		message += "; build site to publish changes"
	}
	s.redirect(w, r, s.link("settings", "publishing"), message)
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
