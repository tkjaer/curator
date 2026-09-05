package build

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/render"
)

const defaultFacetPageSize = 100

const (
	dateArchiveNamespace = "date"
	dateArchiveLabel     = "Date"
)

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
	b.facets = nil
	b.facetGroups = map[string]map[string][]facetPhoto{}
	for _, f := range all {
		if !f.Enabled {
			continue
		}
		b.facets = append(b.facets, f)
		b.facetGroups[f.Namespace] = map[string][]facetPhoto{}
	}
	if dateArchiveDepth(b.settings) > 0 {
		f := model.FacetConfig{Namespace: dateArchiveNamespace, Enabled: true, Source: "DateTimeOriginal", Label: dateArchiveLabel}
		b.facets = append(b.facets, f)
		b.facetGroups[f.Namespace] = map[string][]facetPhoto{}
	}
	return nil
}

// accumulate records a published photo under each enabled facet's values.
func (b *Builder) accumulate(it model.Item, pv render.PhotoView, tags []string) {
	for _, f := range b.facets {
		if f.Namespace == "tag" {
			for _, value := range tags {
				b.accumulateFacet(f.Namespace, value, it, pv)
			}
			continue
		}
		v := facetValue(it, f.Namespace)
		if v == "" {
			continue
		}
		b.accumulateFacet(f.Namespace, v, it, pv)
	}
}

func (b *Builder) accumulateFacet(namespace, value string, it model.Item, pv render.PhotoView) {
	b.facetGroups[namespace][value] = append(b.facetGroups[namespace][value], facetPhoto{
		Photo: pv, TakenAt: it.TakenAt, Filename: it.Filename,
	})
}

func (b *Builder) facetEnabled(namespace string) bool {
	for _, facet := range b.facets {
		if facet.Namespace == namespace {
			return true
		}
	}
	return false
}

func visibleUserTags(tags []string, settings map[string]string) []string {
	mode := settings["metadata.tag_visibility"]
	selected := make(map[string]bool)
	for _, value := range strings.Split(settings["metadata.tag_selection"], "\n") {
		if value = strings.TrimSpace(value); value != "" {
			selected[strings.ToLower(value)] = true
		}
	}

	visible := make([]string, 0, len(tags))
	for _, value := range tags {
		isSelected := selected[strings.ToLower(value)]
		show := true
		switch mode {
		case "hide_all":
			show = false
		case "show_selected":
			show = isSelected
		case "hide_selected":
			show = !isSelected
		}
		if show {
			visible = append(visible, value)
		}
	}
	return visible
}

func facetValue(it model.Item, namespace string) string {
	switch namespace {
	case "camera":
		return it.Camera
	case "lens":
		return it.Lens
	case dateArchiveNamespace:
		if it.TakenAt != nil {
			return it.TakenAt.Format("2006-01-02")
		}
		return ""
	default:
		return ""
	}
}

// renderFacets writes the browse index and per-value pages for each facet.
func (b *Builder) renderFacets() error {
	paginationEnabled, configuredPageSize := facetPaginationSettings(b.settings)
	for _, f := range b.facets {
		groups := b.facetGroups[f.Namespace]
		if f.Namespace == dateArchiveNamespace {
			if err := b.renderDateArchive(groups, dateArchiveDepth(b.settings)); err != nil {
				return err
			}
			continue
		}
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

type dateArchiveGroup struct {
	Segment string
	Title   string
	Photos  []facetPhoto
}

func dateArchiveDepth(settings map[string]string) int {
	if settings["metadata.date_archive_year"] != "true" {
		return 0
	}
	if settings["metadata.date_archive_month"] != "true" {
		return 1
	}
	if settings["metadata.date_archive_day"] != "true" {
		return 2
	}
	return 3
}

func (b *Builder) renderDateArchive(groups map[string][]facetPhoto, depth int) error {
	years := map[string][]facetPhoto{}
	months := map[string]map[string][]facetPhoto{}
	days := map[string]map[string]map[string][]facetPhoto{}
	for value, photos := range groups {
		taken, err := time.Parse("2006-01-02", value)
		if err != nil {
			continue
		}
		year := taken.Format("2006")
		month := taken.Format("01")
		day := taken.Format("02")
		years[year] = append(years[year], photos...)
		if months[year] == nil {
			months[year] = map[string][]facetPhoto{}
		}
		months[year][month] = append(months[year][month], photos...)
		if days[year] == nil {
			days[year] = map[string]map[string][]facetPhoto{}
		}
		if days[year][month] == nil {
			days[year][month] = map[string][]facetPhoto{}
		}
		days[year][month][day] = append(days[year][month][day], photos...)
	}

	yearGroups := make([]dateArchiveGroup, 0, len(years))
	for year, photos := range years {
		yearGroups = append(yearGroups, dateArchiveGroup{Segment: year, Title: year, Photos: photos})
	}
	if err := b.renderDateArchiveIndex(dateArchiveLabel, nil, yearGroups); err != nil {
		return err
	}

	for year, photos := range years {
		yearParts := []string{year}
		if depth == 1 {
			if err := b.renderDateArchivePhotos(year, yearParts, photos); err != nil {
				return err
			}
			continue
		}
		monthGroups := make([]dateArchiveGroup, 0, len(months[year]))
		for month, monthPhotos := range months[year] {
			monthNumber, _ := strconv.Atoi(month)
			monthGroups = append(monthGroups, dateArchiveGroup{
				Segment: month,
				Title:   time.Month(monthNumber).String(),
				Photos:  monthPhotos,
			})
		}
		if err := b.renderDateArchiveIndex(year, yearParts, monthGroups); err != nil {
			return err
		}
		for month, monthPhotos := range months[year] {
			monthNumber, _ := strconv.Atoi(month)
			monthTitle := time.Month(monthNumber).String() + " " + year
			monthParts := []string{year, month}
			if depth == 2 {
				if err := b.renderDateArchivePhotos(monthTitle, monthParts, monthPhotos); err != nil {
					return err
				}
				continue
			}
			dayGroups := make([]dateArchiveGroup, 0, len(days[year][month]))
			for day, dayPhotos := range days[year][month] {
				dayNumber, _ := strconv.Atoi(day)
				dayGroups = append(dayGroups, dateArchiveGroup{
					Segment: day,
					Title:   strconv.Itoa(dayNumber) + " " + time.Month(monthNumber).String(),
					Photos:  dayPhotos,
				})
			}
			if err := b.renderDateArchiveIndex(monthTitle, monthParts, dayGroups); err != nil {
				return err
			}
			for day, dayPhotos := range days[year][month] {
				dayNumber, _ := strconv.Atoi(day)
				title := strconv.Itoa(dayNumber) + " " + monthTitle
				if err := b.renderDateArchivePhotos(title, []string{year, month, day}, dayPhotos); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (b *Builder) renderDateArchiveIndex(title string, parts []string, groups []dateArchiveGroup) error {
	sort.Slice(groups, func(i, j int) bool { return groups[i].Segment > groups[j].Segment })
	items := make([]render.FacetItem, 0, len(groups))
	for _, group := range groups {
		sortFacetPhotos(group.Photos)
		var cover render.Source
		if len(group.Photos) > 0 {
			cover = cardCover(group.Photos[0].Photo)
		}
		items = append(items, render.FacetItem{
			Title: group.Title,
			Href:  b.dateArchiveURL(appendPath(parts, group.Segment)),
			Cover: cover,
			Count: len(group.Photos),
		})
	}
	return b.writeHTML(b.dateArchiveOutput(parts, 1), "facet-index", render.FacetIndexView{
		Title: title, Items: items, Breadcrumb: b.dateArchiveBreadcrumb(parts),
		Options: b.options, Site: b.site,
	})
}

func (b *Builder) renderDateArchivePhotos(title string, parts []string, photos []facetPhoto) error {
	sortFacetPhotos(photos)
	paginationEnabled, configuredPageSize := facetPaginationSettings(b.settings)
	pageSize := len(photos)
	if paginationEnabled {
		pageSize = configuredPageSize
	}
	pageCount := (len(photos) + pageSize - 1) / pageSize
	for page := 1; page <= pageCount; page++ {
		start := (page - 1) * pageSize
		end := min(start+pageSize, len(photos))
		pagePhotos := make([]render.PhotoView, 0, end-start)
		for _, photo := range photos[start:end] {
			pagePhotos = append(pagePhotos, photo.Photo)
		}
		view := render.FacetValueView{
			Title: title,
			Rows: render.Justify(pagePhotos, contentWidth, optInt(b.options, "rowHeight", 300),
				optInt(b.options, "gridGap", 8), optBool(b.options, "panoFullWidth", true)),
			Breadcrumb: b.dateArchiveBreadcrumb(parts),
			Page:       page, PageCount: pageCount, Options: b.options, Site: b.site,
		}
		if page > 1 {
			view.PreviousURL = b.dateArchivePageURL(parts, page-1)
		}
		if page < pageCount {
			view.NextURL = b.dateArchivePageURL(parts, page+1)
		}
		if err := b.writeHTML(b.dateArchiveOutput(parts, page), "facet-value", view); err != nil {
			return err
		}
	}
	return nil
}

func appendPath(parts []string, segment string) []string {
	result := make([]string, len(parts), len(parts)+1)
	copy(result, parts)
	return append(result, segment)
}

func (b *Builder) dateArchiveURL(parts []string) string {
	url := b.site.BaseURL + "/" + browseRoot + "/" + dateArchiveNamespace + "/"
	if len(parts) > 0 {
		url += strings.Join(parts, "/") + "/"
	}
	return url
}

func (b *Builder) dateArchivePageURL(parts []string, page int) string {
	if page <= 1 {
		return b.dateArchiveURL(parts)
	}
	return b.dateArchiveURL(parts) + "page/" + strconv.Itoa(page) + "/"
}

func (b *Builder) dateArchiveOutput(parts []string, page int) string {
	pathParts := []string{b.Cfg.OutputDir, browseRoot, dateArchiveNamespace}
	pathParts = append(pathParts, parts...)
	if page > 1 {
		pathParts = append(pathParts, "page", strconv.Itoa(page))
	}
	pathParts = append(pathParts, "index.html")
	return filepath.Join(pathParts...)
}

func (b *Builder) dateArchiveBreadcrumb(parts []string) []render.Crumb {
	if len(parts) == 0 {
		return nil
	}
	crumbs := []render.Crumb{{Title: dateArchiveLabel, Href: b.dateArchiveURL(nil)}}
	if len(parts) > 1 {
		crumbs = append(crumbs, render.Crumb{Title: parts[0], Href: b.dateArchiveURL(parts[:1])})
	}
	if len(parts) > 2 {
		monthNumber, _ := strconv.Atoi(parts[1])
		crumbs = append(crumbs, render.Crumb{
			Title: time.Month(monthNumber).String() + " " + parts[0],
			Href:  b.dateArchiveURL(parts[:2]),
		})
	}
	return crumbs
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
