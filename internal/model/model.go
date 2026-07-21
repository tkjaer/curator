// Package model holds Curator's domain types. It has no dependencies on storage
// or rendering so any package can share these types without cycles.
package model

import "time"

// GalleryStatus and ItemStatus control what reaches the public output.
// Effective visibility is the more restrictive of a gallery's and item's status.
type GalleryStatus string

const (
	GalleryDraft     GalleryStatus = "draft"
	GalleryUnlisted  GalleryStatus = "unlisted"
	GalleryPublished GalleryStatus = "published"
	GalleryProtected GalleryStatus = "protected"
)

type ItemStatus string

const (
	ItemDraft     ItemStatus = "draft"
	ItemUnlisted  ItemStatus = "unlisted"
	ItemPublished ItemStatus = "published"
)

// GalleryType hints at default rendering. A gallery's body is always a list of
// blocks; a grid gallery is simply one whose blocks are a single grid.
type GalleryType string

const (
	GalleryGrid  GalleryType = "grid"
	GalleryStory GalleryType = "story"
)

// SortMode decides the default ordering of items within a gallery. Manual
// ordering always wins via each item's SortOrder.
type SortMode string

const (
	SortByDate     SortMode = "date"
	SortByFilename SortMode = "filename"
	SortManual     SortMode = "manual"
)

// Aspect classifies an item's shape. Layout uses each item's real pixel
// dimensions, so any ratio (3:2, 4:3, 16:9, …) renders correctly; this
// classification exists only for panorama handling and theme styling.
type Aspect string

const (
	AspectLandscape Aspect = "landscape"
	AspectPortrait  Aspect = "portrait"
	AspectSquare    Aspect = "square"
	AspectPano      Aspect = "pano"
)

// panoRatio is the width/height (or its inverse) at or beyond which an image is
// treated as a panorama. 65:24 ≈ 2.71; 16:9 ≈ 1.78 stays a normal landscape.
const panoRatio = 2.2

// ClassifyAspect derives an Aspect from pixel dimensions.
func ClassifyAspect(width, height int) Aspect {
	if width <= 0 || height <= 0 {
		return AspectLandscape
	}
	r := float64(width) / float64(height)
	switch {
	case r >= panoRatio || r <= 1/panoRatio:
		return AspectPano
	case r > 1.05:
		return AspectLandscape
	case r < 0.95:
		return AspectPortrait
	default:
		return AspectSquare
	}
}

// BlockType is the kind of content a block carries within a gallery.
type BlockType string

const (
	BlockHeading BlockType = "heading"
	BlockText    BlockType = "text"
	BlockQuote   BlockType = "quote"
	BlockImage   BlockType = "image"
	BlockGrid    BlockType = "grid"
)

// Gallery is a node in the gallery tree.
type Gallery struct {
	ID          int64
	ParentID    *int64
	Slug        string
	Title       string
	Description string
	Type        GalleryType
	Status      GalleryStatus
	CoverItemID *int64
	SortMode    SortMode
	SortOrder   int
	Theme       string
	ShowEXIF    bool
	PublishedAt *time.Time
}

// Item is a single photo. The original file is immutable; derivatives are a
// rebuildable cache keyed by content hash.
type Item struct {
	ID           int64
	GalleryID    int64
	OriginalPath string
	Filename     string
	Width        int
	Height       int
	Aspect       Aspect
	Highlighted  bool
	SortOrder    int
	Status       ItemStatus
	Caption      string
	EXIF         string // raw EXIF JSON
	Camera       string
	Lens         string
	Aperture     string
	Shutter      string
	ISO          string
	Focal        string
	TakenAt      *time.Time
}

// Derivative is a generated size of an item's image.
type Derivative struct {
	ID     int64
	ItemID int64
	Preset string
	Width  int
	Height int
	Path   string
	Hash   string
}

// Block is one ordered piece of a gallery's body.
type Block struct {
	ID        int64
	GalleryID int64
	Type      BlockType
	ItemID    *int64
	Content   string // markdown for text/heading/quote
	SortOrder int
}

// Tag labels items. Namespaces separate user tags from derived facets such as
// camera, lens, and aspect.
type Tag struct {
	ID        int64
	Namespace string
	Value     string
}

// FacetConfig controls an opt-in browse facet such as camera or lens.
type FacetConfig struct {
	Namespace string
	Enabled   bool
	Source    string
	Label     string
}

// AccessUser is a basic-auth credential for protected galleries.
type AccessUser struct {
	ID       int64
	Username string
	Hash     string
}

// Preset describes one derivative size. Kind is "width" (scale to fit a maximum
// width) or "cover" (scale and center-crop to exact dimensions, for thumbnails).
type Preset struct {
	Name      string
	Kind      string
	MaxWidth  int
	MaxHeight int
	Quality   int
}
