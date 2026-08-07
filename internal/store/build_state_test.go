package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tkjaer/curator/internal/model"
)

func TestBuildStateMigrationSkipsHistoricalTagVersions(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "cms.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.DB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		INSERT INTO schema_migrations (version) VALUES (21), (22)`); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BuildRevision(ctx); err != nil {
		t.Fatalf("build-state migration was not applied: %v", err)
	}
}

func TestBuildRevisionTracksInputsButNotDerivativeCache(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	initial, err := st.BuildRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	galleryID, err := st.CreateGallery(ctx, model.Gallery{Slug: "photos", Title: "Photos"})
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := st.CreateItem(ctx, model.Item{
		GalleryID: galleryID, OriginalPath: "photos/a.jpg", Filename: "a.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceItemUserTags(ctx, itemID, []string{"night"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "site.title", "Changed"); err != nil {
		t.Fatal(err)
	}
	changed, err := st.BuildRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed <= initial {
		t.Fatalf("revision = %d after input changes, want greater than %d", changed, initial)
	}
	if err := st.SetSetting(ctx, "publish.rsync_target", "photos@example.com:/srv/site"); err != nil {
		t.Fatal(err)
	}
	afterPublishSetting, err := st.BuildRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterPublishSetting != changed {
		t.Fatalf("deployment setting changed revision from %d to %d", changed, afterPublishSetting)
	}

	if err := st.UpsertDerivative(ctx, model.Derivative{
		ItemID: itemID, Preset: "thumb", Width: 400, Height: 300,
		Path: "_curator/img/test.jpg", Hash: "test",
	}); err != nil {
		t.Fatal(err)
	}
	afterDerivative, err := st.BuildRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterDerivative != changed {
		t.Fatalf("derivative cache changed revision from %d to %d", changed, afterDerivative)
	}

	want := BuildState{ContentRevision: changed, Fingerprint: "theme", Galleries: 1, Photos: 1}
	if err := st.SetBuildState(ctx, "/tmp/output", want); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.BuildState(ctx, "/tmp/output")
	if err != nil {
		t.Fatal(err)
	}
	if !found || got != want {
		t.Fatalf("build state = %#v, %v; want %#v, true", got, found, want)
	}
}
