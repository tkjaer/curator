// Package build renders the site to static files: it reads published galleries
// and items from the store, generates image derivatives, computes justified
// grids, renders through the active theme, and writes the output directory.
package build

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/ingest"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/render"
	"github.com/tkjaer/curator/internal/slug"
	"github.com/tkjaer/curator/internal/store"
	"github.com/tkjaer/curator/internal/theme"
)

const contentWidth = 1200

// Progress reports how far along a build is. Total is 0 for stages without a
// known count.
type Progress struct {
	Stage string // "images" | "render" | "finishing"
	Done  int
	Total int
}

// Report summarizes a completed build.
type Report struct {
	Galleries   int
	Photos      int
	Generated   int // image derivatives written this build
	Reused      int // derivatives already on disk
	FeedUpdated bool
	Duration    time.Duration
}

// Builder renders a site from the store through a theme into the output dir.
type Builder struct {
	Store *store.Store
	Theme *theme.Theme
	Cfg   config.Config

	// OnProgress, if set, is called as the build advances.
	OnProgress func(Progress)

	site    render.SiteView
	options map[string]any
	byID    map[int64]model.Gallery

	settings    map[string]string
	lensPolicy  ingest.LensPolicy
	facets      []model.FacetConfig
	facetGroups map[string]map[string][]render.PhotoView
	kept        map[string]bool

	report     Report
	processed  int
	totalItems int
}

// New returns a Builder.
func New(st *store.Store, th *theme.Theme, cfg config.Config) *Builder {
	return &Builder{Store: st, Theme: th, Cfg: cfg}
}

func (b *Builder) progress(stage string, done, total int) {
	if b.OnProgress != nil {
		b.OnProgress(Progress{Stage: stage, Done: done, Total: total})
	}
}

// Build renders the site, discarding the summary report. Convenience wrapper
// around BuildReport.
func (b *Builder) Build(ctx context.Context) error {
	_, err := b.BuildReport(ctx)
	return err
}

// BuildReport writes the full static site to the configured output directory and
// returns a summary report.
func (b *Builder) BuildReport(ctx context.Context) (Report, error) {
	start := time.Now()
	b.report = Report{}
	b.processed = 0
	settings, err := b.Store.Settings(ctx)
	if err != nil {
		return Report{}, err
	}
	lensPolicy, err := ingest.LensPolicyFromSettings(settings)
	if err != nil {
		return Report{}, err
	}
	presets, err := b.Store.Presets(ctx)
	if err != nil {
		return Report{}, err
	}
	galleries, err := b.Store.Galleries(ctx)
	if err != nil {
		return Report{}, err
	}
	b.totalItems, _ = b.Store.CountPublishedItems(ctx)

	b.options = b.Theme.Manifest.Defaults()
	b.byID = make(map[int64]model.Gallery, len(galleries))
	for _, g := range galleries {
		b.byID[g.ID] = g
	}
	b.kept = map[string]bool{}
	b.lensPolicy = lensPolicy

	if err := b.loadFacets(ctx); err != nil {
		return Report{}, err
	}

	b.settings = settings
	visible, children, roots, protected := groupVisible(galleries)
	b.report.Galleries = len(visible)

	b.site = render.SiteView{
		Title:     settings["site.title"],
		BaseURL:   strings.TrimRight(settings["site.base_url"], "/"),
		Copyright: copyrightLine(settings, time.Now().Year()),
	}
	if settings["site.feed_enabled"] == "true" && b.site.BaseURL != "" {
		b.site.FeedURL = b.site.BaseURL + "/feed.xml"
	}
	for _, g := range roots {
		b.site.Nav = append(b.site.Nav, render.NavNode{Title: g.Title, Href: b.urlPath(g.ID)})
	}
	for _, f := range b.facets {
		b.site.Facets = append(b.site.Facets, render.FacetLink{Label: f.Label, Href: b.browseURL(f.Namespace)})
	}

	// Pass 1: generate derivatives and photo views, and a cover per gallery.
	photos := make(map[int64][]render.PhotoView, len(visible))
	byItem := make(map[int64]map[int64]render.PhotoView, len(visible))
	covers := make(map[int64]render.Source, len(visible))
	for _, g := range visible {
		views, items, err := b.galleryPhotos(ctx, g, presets)
		if err != nil {
			return Report{}, fmt.Errorf("gallery %q: %w", g.Slug, err)
		}
		photos[g.ID] = views
		byItem[g.ID] = items
		// Protected galleries get no public cover: the thumbnail would live behind
		// auth and only break in public listings.
		if g.Status == model.GalleryProtected {
			continue
		}
		if g.CoverItemID != nil {
			if pv, ok := items[*g.CoverItemID]; ok {
				covers[g.ID] = cardCover(pv)
			}
		}
		if _, has := covers[g.ID]; !has && len(views) > 0 {
			covers[g.ID] = cardCover(views[0])
		}
	}

	// Fall back to a nested gallery's cover for folders with no image of their
	// own, using only published (non-hidden, non-protected) descendants.
	b.resolveNestedCovers(visible, covers)

	// Pass 2: render each gallery page.
	for i, g := range visible {
		b.progress("render", i+1, len(visible))
		if err := b.renderGallery(ctx, g, photos[g.ID], byItem[g.ID], children[g.ID], covers); err != nil {
			return Report{}, fmt.Errorf("render %q: %w", g.Slug, err)
		}
	}

	b.progress("finishing", 0, 0)
	if err := b.renderIndex(roots, photos, covers); err != nil {
		return Report{}, err
	}
	if err := b.renderFacets(); err != nil {
		return Report{}, err
	}
	if err := b.copyAssets(); err != nil {
		return Report{}, err
	}
	if err := b.emitAuth(ctx, protected); err != nil {
		return Report{}, err
	}
	if err := b.emitFeed(visible); err != nil {
		return Report{}, err
	}
	if err := b.fillDirectoryIndexes(); err != nil {
		return Report{}, err
	}
	if err := b.sweep(); err != nil {
		return Report{}, err
	}

	b.report.Duration = time.Since(start)
	return b.report, nil
}

func copyrightLine(settings map[string]string, currentYear int) string {
	holder := strings.TrimSpace(settings["site.copyright_holder"])
	if holder == "" {
		return ""
	}
	year, err := strconv.Atoi(settings["site.copyright_start_year"])
	if err != nil || year <= 0 || year >= currentYear {
		return fmt.Sprintf("© %d %s", currentYear, holder)
	}
	return fmt.Sprintf("© %d–%d %s", year, currentYear, holder)
}

func (b *Builder) renderGallery(ctx context.Context, g model.Gallery, pics []render.PhotoView, byItem map[int64]render.PhotoView, kids []model.Gallery, covers map[int64]render.Source) error {
	view := render.GalleryView{
		Title:      g.Title,
		Slug:       g.Slug,
		Type:       string(g.Type),
		Breadcrumb: b.breadcrumb(g.ID),
		Children:   b.cards(kids, covers, nil),
		Options:    b.options,
		Site:       b.site,
	}

	if g.Type == model.GalleryStory {
		blocks, err := b.Store.BlocksByGallery(ctx, g.ID)
		if err != nil {
			return err
		}
		view.Blocks, err = b.buildBlocks(ctx, blocks, byItem)
		if err != nil {
			return err
		}
		return b.writeHTML(b.outputPath(g.ID), "gallery-story", view)
	}

	view.Rows = render.Justify(pics, contentWidth, optInt(b.options, "rowHeight", 300),
		optInt(b.options, "gridGap", 8), optBool(b.options, "panoFullWidth", true))
	return b.writeHTML(b.outputPath(g.ID), "gallery-grid", view)
}

func (b *Builder) renderIndex(roots []model.Gallery, photos map[int64][]render.PhotoView, covers map[int64]render.Source) error {
	counts := make(map[int64]int, len(photos))
	for id, p := range photos {
		counts[id] = len(p)
	}
	view := render.GalleryView{
		Title:    b.site.Title,
		Children: b.cards(roots, covers, counts),
		Options:  b.options,
		Site:     b.site,
	}
	return b.writeHTML(filepath.Join(b.Cfg.OutputDir, "index.html"), "gallery-list", view)
}

func (b *Builder) cards(galleries []model.Gallery, covers map[int64]render.Source, counts map[int64]int) []render.GalleryCard {
	var out []render.GalleryCard
	for _, g := range galleries {
		out = append(out, render.GalleryCard{
			Title:  g.Title,
			Href:   b.urlPath(g.ID),
			Cover:  covers[g.ID],
			Count:  counts[g.ID],
			Locked: g.Status == model.GalleryProtected,
		})
	}
	return out
}

func cardCover(photo render.PhotoView) render.Source {
	var best render.Source
	for _, source := range photo.Srcset {
		if source.Width >= 800 && (best.URL == "" || source.Width < best.Width) {
			best = source
		}
	}
	if best.URL != "" {
		return best
	}
	for _, source := range photo.Srcset {
		if source.Width > best.Width {
			best = source
		}
	}
	if best.URL != "" {
		return best
	}
	return photo.Thumb
}

func (b *Builder) breadcrumb(id int64) []render.Crumb {
	var chain []render.Crumb
	for cur, ok := b.byID[id]; ok; cur, ok = b.parent(cur) {
		chain = append([]render.Crumb{{Title: cur.Title, Href: b.urlPath(cur.ID)}}, chain...)
		if cur.ParentID == nil {
			break
		}
	}
	return chain
}

func (b *Builder) parent(g model.Gallery) (model.Gallery, bool) {
	if g.ParentID == nil {
		return model.Gallery{}, false
	}
	p, ok := b.byID[*g.ParentID]
	return p, ok
}

// urlPath is the site-relative URL of a gallery, e.g. /galleries/trip/day-one/.
func (b *Builder) urlPath(id int64) string {
	var segs []string
	for cur, ok := b.byID[id]; ok; {
		segs = append([]string{cur.Slug}, segs...)
		if cur.ParentID == nil {
			break
		}
		cur, ok = b.byID[*cur.ParentID]
	}
	return b.site.BaseURL + "/galleries/" + strings.Join(segs, "/") + "/"
}

func (b *Builder) outputPath(id int64) string {
	rel := strings.TrimPrefix(b.urlPath(id), b.site.BaseURL+"/")
	return filepath.Join(b.Cfg.OutputDir, filepath.FromSlash(rel), "index.html")
}

func (b *Builder) browseURL(namespace string) string {
	return b.site.BaseURL + "/browse/" + namespace + "/"
}

func (b *Builder) browseValueURL(namespace, value string) string {
	return b.browseURL(namespace) + slug.Make(value) + "/"
}

func (b *Builder) browseIndexOutput(namespace string) string {
	return filepath.Join(b.Cfg.OutputDir, "browse", namespace, "index.html")
}

func (b *Builder) browseValueOutput(namespace, value string) string {
	return filepath.Join(b.Cfg.OutputDir, "browse", namespace, slug.Make(value), "index.html")
}

func (b *Builder) writeHTML(path, tmpl string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := b.Theme.Render(f, tmpl, data); err != nil {
		return err
	}
	b.keep(path)
	return f.Close()
}

func (b *Builder) copyAssets() error {
	assets, err := b.Theme.Assets()
	if err != nil {
		return err
	}
	destRoot := filepath.Join(b.Cfg.OutputDir, "assets")

	return fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		dest := filepath.Join(destRoot, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		in, err := assets.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		defer out.Close()
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		b.keep(dest)
		return out.Close()
	})
}

// keep records an output file (by its path) so the post-build sweep does not
// remove it.
func (b *Builder) keep(path string) {
	rel, err := filepath.Rel(b.Cfg.OutputDir, path)
	if err != nil {
		return
	}
	b.kept[filepath.ToSlash(rel)] = true
}

// fillDirectoryIndexes writes a themed placeholder index.html into every
// ancestor directory of a built file that lacks a real page. This stops the web
// server from serving an autoindex listing (which would leak unlisted folders)
// when a visitor strips a slug off a URL.
func (b *Builder) fillDirectoryIndexes() error {
	dirs := map[string]bool{}
	for rel := range b.kept {
		for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}

	for dir := range dirs {
		if b.kept[dir+"/index.html"] {
			continue // a real page already lives here
		}
		out := filepath.Join(b.Cfg.OutputDir, filepath.FromSlash(dir), "index.html")
		if err := b.writeHTML(out, "empty", render.GalleryView{
			Title: "Not found", Options: b.options, Site: b.site,
		}); err != nil {
			return err
		}
	}
	return nil
}

// sweep removes any file under the output directory that this build did not
// write, then prunes empty directories. It runs only after a successful build.
func (b *Builder) sweep() error {
	var orphans []string
	err := filepath.WalkDir(b.Cfg.OutputDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(b.Cfg.OutputDir, path)
		if err != nil {
			return err
		}
		if !b.kept[filepath.ToSlash(rel)] {
			orphans = append(orphans, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range orphans {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return pruneEmptyDirs(b.Cfg.OutputDir)
}

// pruneEmptyDirs removes empty directories under root (root itself is kept).
func pruneEmptyDirs(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if err := pruneEmptyDirs(dir); err != nil {
			return err
		}
		if remaining, err := os.ReadDir(dir); err == nil && len(remaining) == 0 {
			if err := os.Remove(dir); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveNestedCovers fills in a cover for folder galleries that have no image
// of their own, using the first published descendant gallery's cover.
func (b *Builder) resolveNestedCovers(visible []model.Gallery, covers map[int64]render.Source) {
	publishedChildren := map[int64][]model.Gallery{}
	for _, g := range visible {
		if g.Status == model.GalleryPublished && g.ParentID != nil {
			publishedChildren[*g.ParentID] = append(publishedChildren[*g.ParentID], g)
		}
	}

	var resolve func(id int64) render.Source
	resolve = func(id int64) render.Source {
		if c, ok := covers[id]; ok {
			return c
		}
		for _, child := range publishedChildren[id] {
			if c := resolve(child.ID); c.URL != "" {
				covers[id] = c // memoize
				return c
			}
		}
		return render.Source{}
	}

	for _, g := range visible {
		if _, ok := covers[g.ID]; !ok {
			resolve(g.ID)
		}
	}
}

// groupVisible collects galleries that are built. Published and protected
// galleries are also linked (from nav and parent listings); unlisted galleries
// are built and reachable by direct URL but never linked. Protected galleries
// are returned separately so their access files can be emitted.
func groupVisible(all []model.Gallery) (visible []model.Gallery, children map[int64][]model.Gallery, roots, protected []model.Gallery) {
	children = map[int64][]model.Gallery{}
	for _, g := range all {
		switch g.Status {
		case model.GalleryPublished, model.GalleryProtected, model.GalleryUnlisted:
			visible = append(visible, g)
		default:
			continue
		}

		if g.Status == model.GalleryUnlisted {
			continue // built, but not linked anywhere
		}
		if g.Status == model.GalleryProtected {
			protected = append(protected, g)
		}
		if g.ParentID == nil {
			roots = append(roots, g)
		} else {
			children[*g.ParentID] = append(children[*g.ParentID], g)
		}
	}
	return visible, children, roots, protected
}

func optInt(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return def
	}
}

func optBool(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}
