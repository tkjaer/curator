package build

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tkjaer/curator/internal/model"
)

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Xmlns   string      `xml:"xmlns,attr"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	ID        string     `xml:"id"`
	Updated   string     `xml:"updated"`
	Published string     `xml:"published"`
	Links     []atomLink `xml:"link"`
	Summary   string     `xml:"summary,omitempty"`
}

// emitFeed writes an Atom 1.0 feed of published galleries, newest first, when
// the feed is enabled. Only listed, published galleries with a publication date
// are included.
func (b *Builder) emitFeed(visible []model.Gallery) error {
	if b.site.FeedURL == "" {
		return nil
	}

	var published []model.Gallery
	for _, g := range visible {
		if g.Status == model.GalleryPublished && g.PublishedAt != nil {
			published = append(published, g)
		}
	}
	sort.Slice(published, func(i, j int) bool {
		return published[i].PublishedAt.After(*published[j].PublishedAt)
	})

	updated := time.Now().UTC()
	if len(published) > 0 {
		updated = published[0].PublishedAt.UTC()
	}

	feed := atomFeed{
		Xmlns:   "http://www.w3.org/2005/Atom",
		Title:   b.site.Title,
		ID:      b.site.BaseURL + "/",
		Updated: updated.Format(time.RFC3339),
		Links: []atomLink{
			{Href: b.site.BaseURL + "/"},
			{Href: b.site.FeedURL, Rel: "self"},
		},
	}
	for _, g := range published {
		url := b.urlPath(g.ID)
		stamp := g.PublishedAt.UTC().Format(time.RFC3339)
		feed.Entries = append(feed.Entries, atomEntry{
			Title:     g.Title,
			ID:        url,
			Updated:   stamp,
			Published: stamp,
			Links:     []atomLink{{Href: url}},
			Summary:   g.Description,
		})
	}

	body, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return err
	}
	dest := filepath.Join(b.Cfg.OutputDir, "feed.xml")
	if err := os.WriteFile(dest, append([]byte(xml.Header), body...), 0o644); err != nil {
		return err
	}
	b.keep(dest)
	b.report.FeedUpdated = true
	return nil
}
