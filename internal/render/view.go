// Package render defines the view models that themes receive and the pure
// layout logic used to build them. Templates depend only on these types, never
// on storage, so the theme contract stays stable as internals change.
package render

import (
	"html/template"
	"strings"
)

// Source is one rendered image size.
type Source struct {
	URL    string
	Width  int
	Height int
}

// ExifView is the optional camera metadata shown on a photo. It is nil when a
// gallery hides EXIF.
type ExifView struct {
	Camera   string
	Lens     string
	Aperture string
	Shutter  string
	ISO      string
	Focal    string
	TakenAt  string
}

// Line renders the metadata as a compact lightbox summary.
func (e ExifView) Line() string {
	var parts []string
	if e.Camera != "" {
		parts = append(parts, e.Camera)
	}
	if e.Lens != "" {
		parts = append(parts, e.Lens)
	}
	if exposure := strings.TrimSpace(e.Aperture + " " + e.Shutter); exposure != "" {
		parts = append(parts, exposure)
	}
	if e.ISO != "" {
		parts = append(parts, "ISO "+e.ISO)
	}
	if e.Focal != "" {
		parts = append(parts, e.Focal)
	}
	return strings.Join(parts, " · ")
}

// PhotoView is a single image ready to render. FlexBasis is the photo's width
// as a percentage of its justified row; RowHeight is that row's height in px.
type PhotoView struct {
	Slug        string
	Caption     string
	Alt         string
	Width       int
	Height      int
	Aspect      string
	Highlighted bool
	Thumb       Source
	Display     Source
	Srcset      []Source
	Href        string
	Exif        *ExifView
	FlexBasis   float64
	RowHeight   int
}

// GridRow is one row of a justified grid. Center is set for rows that do not
// fill the container width (a capped panorama) so the theme can center them.
type GridRow struct {
	Photos []PhotoView
	Height int
	Center bool
}

// GalleryCard represents a sub-gallery in a folder listing.
type GalleryCard struct {
	Title  string
	Href   string
	Cover  Source
	Count  int
	Locked bool
}

// Crumb is one step in a breadcrumb trail.
type Crumb struct {
	Title string
	Href  string
}

// BlockView is one piece of a gallery body. Only the field matching Type is set.
type BlockView struct {
	Type  string
	HTML  template.HTML
	Photo *PhotoView
	Rows  []GridRow
}

// NavNode is one entry in the site navigation tree.
type NavNode struct {
	Title    string
	Href     string
	Children []NavNode
}

// FacetLink points at a facet index such as "browse by camera".
type FacetLink struct {
	Label string
	Href  string
}

// SiteView is the site-wide context available to every page.
type SiteView struct {
	Title     string
	BaseURL   string
	FeedURL   string
	Copyright string
	Nav       []NavNode
	Facets    []FacetLink
}

// GalleryView is the model passed to a gallery template. Grid galleries use
// Rows; story galleries use Blocks.
type GalleryView struct {
	Title       string
	Slug        string
	Description template.HTML
	Type        string
	Breadcrumb  []Crumb
	Children    []GalleryCard
	Rows        []GridRow
	Blocks      []BlockView
	Options     map[string]any
	Site        SiteView
}

// FacetItem is one value within a facet, e.g. a single camera model. Its fields
// mirror GalleryCard so both share the "cards" template.
type FacetItem struct {
	Title  string
	Href   string
	Cover  Source
	Count  int
	Locked bool
}

// FacetIndexView lists the values of one facet (e.g. all cameras).
type FacetIndexView struct {
	Title   string
	Items   []FacetItem
	Options map[string]any
	Site    SiteView
}

// FacetValueView shows the photos for a single facet value.
type FacetValueView struct {
	Title       string
	Rows        []GridRow
	Page        int
	PageCount   int
	PreviousURL string
	NextURL     string
	Options     map[string]any
	Site        SiteView
}
