package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/store"
	"github.com/tkjaer/curator/internal/theme"
)

// buildFeedFixture creates a store with one published gallery (its published_at
// stamped) and applies the given settings, then builds the site.
func buildFeedFixture(t *testing.T, settings map[string]string) (config.Config, Report) {
	t.Helper()
	tmp := t.TempDir()
	cfg := config.New(tmp, filepath.Join(tmp, "output"))
	ctx := context.Background()

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for k, v := range settings {
		if err := st.SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}

	gid, err := st.CreateGallery(ctx, model.Gallery{
		Slug: "trip", Title: "Trip", Description: "A trip.", Type: model.GalleryGrid, Status: model.GalleryDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Publish via the store so published_at is stamped.
	if err := st.UpdateGalleryStatus(ctx, gid, model.GalleryPublished); err != nil {
		t.Fatal(err)
	}
	writeSourceImage(t, filepath.Join(cfg.OriginalsDir(), "trip", "p.jpg"), 7)
	if _, err := st.CreateItem(ctx, model.Item{
		GalleryID: gid, OriginalPath: filepath.Join("trip", "p.jpg"), Filename: "p.jpg",
		Width: 600, Height: 400, Aspect: model.AspectLandscape, Status: model.ItemPublished,
	}); err != nil {
		t.Fatal(err)
	}

	th, err := theme.Load(os.DirFS("../../themes/default"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := New(st, th, cfg).BuildReport(ctx)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return cfg, report
}

func TestFeedGeneratedWhenEnabled(t *testing.T) {
	cfg, report := buildFeedFixture(t, map[string]string{
		"site.feed_enabled": "true",
		"site.base_url":     "https://ex.com",
	})
	if !report.FeedUpdated {
		t.Fatal("build report did not include Atom feed update")
	}

	body, err := os.ReadFile(filepath.Join(cfg.OutputDir, "feed.xml"))
	if err != nil {
		t.Fatalf("expected feed.xml: %v", err)
	}
	feed := string(body)
	for _, want := range []string{
		"<feed xmlns=\"http://www.w3.org/2005/Atom\">",
		"<entry>",
		"<title>Trip</title>",
		"https://ex.com/galleries/trip/",
		"<summary>A trip.</summary>",
	} {
		if !strings.Contains(feed, want) {
			t.Errorf("feed.xml missing %q\n%s", want, feed)
		}
	}

	// The gallery pages should advertise the feed for autodiscovery.
	index, err := os.ReadFile(filepath.Join(cfg.OutputDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `type="application/atom+xml"`) {
		t.Error("index.html should link the feed for autodiscovery")
	}
}

func TestFeedNotWrittenWhenDisabled(t *testing.T) {
	cfg, report := buildFeedFixture(t, map[string]string{
		"site.base_url": "https://ex.com",
	})
	if report.FeedUpdated {
		t.Fatal("build report included disabled Atom feed")
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "feed.xml")); !os.IsNotExist(err) {
		t.Errorf("feed.xml should not exist when the feed is disabled (err=%v)", err)
	}
}

func TestFeedRequiresBaseURL(t *testing.T) {
	cfg, report := buildFeedFixture(t, map[string]string{
		"site.feed_enabled": "true",
	})
	if report.FeedUpdated {
		t.Fatal("build report included Atom feed without Base URL")
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "feed.xml")); !os.IsNotExist(err) {
		t.Errorf("feed.xml should not exist without a base URL (err=%v)", err)
	}
}
