package build

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/render"
)

const defaultFacetPageSize = 100

type facetPhoto struct {
	Photo    render.PhotoView
	TakenAt  *time.Time
	Filename string
}

// loadFacets reads the enabled browse facets and prepares their accumulators.
func (b *Builder) loadFacets(ctx context.Context) error {
	all, err := b.Store.FacetConfigs(ctx)
	if err != nil {
		return err
	}
	b.facetGroups = map[string]map[string][]facetPhoto{}
	for _, f := range all {
		if !f.Enabled {
			continue
		}
		b.facets = append(b.facets, f)
		b.facetGroups[f.Namespace] = map[string][]facetPhoto{}
	}
	return nil
}

// accumulate records a published photo under each enabled facet's value.
func (b *Builder) accumulate(it model.Item, pv render.PhotoView) {
	for _, f := range b.facets {
		v := facetValue(it, f.Namespace)
		if v == "" {
			continue
		}
		b.facetGroups[f.Namespace][v] = append(b.facetGroups[f.Namespace][v], facetPhoto{
			Photo: pv, TakenAt: it.TakenAt, Filename: it.Filename,
		})
	}
}

func facetValue(it model.Item, namespace string) string {
	switch namespace {
	case "camera":
		return it.Camera
	case "lens":
		return it.Lens
	default:
		return ""
	}
}

// renderFacets writes the browse index and per-value pages for each facet.
func (b *Builder) renderFacets() error {
	paginationEnabled, configuredPageSize := facetPaginationSettings(b.settings)
	for _, f := range b.facets {
		groups := b.facetGroups[f.Namespace]
		values := make([]string, 0, len(groups))
		for v := range groups {
			values = append(values, v)
		}
		sort.Strings(values)

		var items []render.FacetItem
		for _, v := range values {
			pics := groups[v]
			sortFacetPhotos(pics)
			pageSize := len(pics)
			if paginationEnabled {
				pageSize = configuredPageSize
			}
			pageCount := (len(pics) + pageSize - 1) / pageSize
			for page := 1; page <= pageCount; page++ {
				start := (page - 1) * pageSize
				end := min(start+pageSize, len(pics))
				pagePhotos := make([]render.PhotoView, 0, end-start)
				for _, pic := range pics[start:end] {
					pagePhotos = append(pagePhotos, pic.Photo)
				}
				rows := render.Justify(pagePhotos, contentWidth, optInt(b.options, "rowHeight", 300),
					optInt(b.options, "gridGap", 8), optBool(b.options, "panoFullWidth", true))
				valueView := render.FacetValueView{
					Title: f.Label + ": " + v, Rows: rows, Page: page, PageCount: pageCount,
					Options: b.options, Site: b.site,
				}
				if page > 1 {
					valueView.PreviousURL = b.browseValuePageURL(f.Namespace, v, page-1)
				}
				if page < pageCount {
					valueView.NextURL = b.browseValuePageURL(f.Namespace, v, page+1)
				}
				if err := b.writeHTML(b.browseValuePageOutput(f.Namespace, v, page), "facet-value", valueView); err != nil {
					return err
				}
			}

			var cover render.Source
			if len(pics) > 0 {
				cover = cardCover(pics[0].Photo)
			}
			items = append(items, render.FacetItem{
				Title: v,
				Href:  b.browseValueURL(f.Namespace, v),
				Cover: cover,
				Count: len(pics),
			})
		}

		indexView := render.FacetIndexView{
			Title:   f.Label,
			Items:   items,
			Options: b.options,
			Site:    b.site,
		}
		if err := b.writeHTML(b.browseIndexOutput(f.Namespace), "facet-index", indexView); err != nil {
			return err
		}
	}
	return nil
}

func facetPaginationSettings(settings map[string]string) (bool, int) {
	enabled := settings["metadata.facet_pagination_enabled"] != "false"
	pageSize, err := strconv.Atoi(settings["metadata.facet_page_size"])
	if err != nil || pageSize < 1 {
		pageSize = defaultFacetPageSize
	}
	return enabled, pageSize
}

func sortFacetPhotos(photos []facetPhoto) {
	sort.SliceStable(photos, func(i, j int) bool {
		left, right := photos[i], photos[j]
		switch {
		case left.TakenAt == nil && right.TakenAt != nil:
			return false
		case left.TakenAt != nil && right.TakenAt == nil:
			return true
		case left.TakenAt != nil && right.TakenAt != nil && !left.TakenAt.Equal(*right.TakenAt):
			return left.TakenAt.After(*right.TakenAt)
		default:
			return strings.Compare(strings.ToLower(left.Filename), strings.ToLower(right.Filename)) > 0
		}
	})
}

// exifView builds the metadata shown on a photo, or nil when nothing is known.
func exifView(it model.Item) *render.ExifView {
	e := &render.ExifView{
		Camera:   it.Camera,
		Lens:     it.Lens,
		Aperture: it.Aperture,
		Shutter:  it.Shutter,
		ISO:      it.ISO,
		Focal:    it.Focal,
	}
	if it.TakenAt != nil {
		e.TakenAt = it.TakenAt.Format("2006-01-02")
	}
	if *e == (render.ExifView{}) {
		return nil
	}
	return e
}
