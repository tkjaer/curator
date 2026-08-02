package build

import (
	"context"
	"sort"

	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/render"
)

// loadFacets reads the enabled browse facets and prepares their accumulators.
func (b *Builder) loadFacets(ctx context.Context) error {
	all, err := b.Store.FacetConfigs(ctx)
	if err != nil {
		return err
	}
	b.facetGroups = map[string]map[string][]render.PhotoView{}
	for _, f := range all {
		if !f.Enabled {
			continue
		}
		b.facets = append(b.facets, f)
		b.facetGroups[f.Namespace] = map[string][]render.PhotoView{}
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
		b.facetGroups[f.Namespace][v] = append(b.facetGroups[f.Namespace][v], pv)
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
			rows := render.Justify(pics, contentWidth, optInt(b.options, "rowHeight", 300),
				optInt(b.options, "gridGap", 8), optBool(b.options, "panoFullWidth", true))
			valueView := render.FacetValueView{
				Title:   f.Label + ": " + v,
				Rows:    rows,
				Options: b.options,
				Site:    b.site,
			}
			if err := b.writeHTML(b.browseValueOutput(f.Namespace, v), "facet-value", valueView); err != nil {
				return err
			}

			var cover render.Source
			if len(pics) > 0 {
				cover = cardCover(pics[0])
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
