package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tkjaer/curator/internal/model"
)

func TestReplaceItemUserTagsNormalizesAndReplaces(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, itemIDs := makeGalleryWithItems(t, st, 1)
	itemID := itemIDs[0]

	if err := st.ReplaceItemUserTags(ctx, itemID, []string{" night-life ", "Night Life", "Kodak   Portra 400", "Sjöstad", "Sjo\u0308stad", ""}); err != nil {
		t.Fatal(err)
	}
	tags, err := st.ItemUserTags(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 3 || tags[0].Value != "kodak portra 400" || tags[1].Value != "night life" || tags[2].Value != "sjöstad" {
		t.Fatalf("tags = %#v, want kodak portra 400, night life, and sjöstad", tags)
	}

	if err := st.ReplaceItemUserTags(ctx, itemID, []string{"Stockholm"}); err != nil {
		t.Fatal(err)
	}
	tags, err = st.ItemUserTags(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Value != "stockholm" {
		t.Fatalf("replacement tags = %#v, want stockholm", tags)
	}

	var orphanCount int
	if err := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM tags WHERE namespace = 'user' AND value IN ('night life', 'kodak portra 400')`).Scan(&orphanCount); err != nil {
		t.Fatal(err)
	}
	if orphanCount != 0 {
		t.Fatalf("orphan user tags = %d, want 0", orphanCount)
	}
}

func TestReplaceItemUserTagsPreservesOtherNamespaces(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, itemIDs := makeGalleryWithItems(t, st, 1)
	itemID := itemIDs[0]

	res, err := st.DB.ExecContext(ctx, `INSERT INTO tags (namespace, value) VALUES ('camera', 'Example Camera')`)
	if err != nil {
		t.Fatal(err)
	}
	tagID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO tag_map (tag_id, item_id) VALUES (?, ?)`, tagID, itemID); err != nil {
		t.Fatal(err)
	}

	if err := st.ReplaceItemUserTags(ctx, itemID, []string{"night"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM tag_map WHERE tag_id = ? AND item_id = ?`, tagID, itemID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("camera tag associations = %d, want 1", count)
	}
}

func TestReplaceItemImportedTagsPreservesManualAssignments(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, itemIDs := makeGalleryWithItems(t, st, 1)
	itemID := itemIDs[0]

	if err := st.ReplaceItemUserTags(ctx, itemID, []string{"curator", "shared"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceItemImportedTags(ctx, itemID, TagSourceMetadata, []string{"XMP", "shared"}); err != nil {
		t.Fatal(err)
	}
	assertTagValues(t, st, ctx, itemID, []string{"curator", "shared", "xmp"})

	if err := st.ReplaceItemImportedTags(ctx, itemID, TagSourceMetadata, []string{"updated"}); err != nil {
		t.Fatal(err)
	}
	assertTagValues(t, st, ctx, itemID, []string{"curator", "shared", "updated"})
	manual, err := st.ItemManualTags(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(manual) != 2 || manual[0].Value != "curator" || manual[1].Value != "shared" {
		t.Fatalf("manual tags = %#v", manual)
	}
}

func TestTagUsagesCountsEffectiveAssignments(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	publishedGallery, err := st.CreateGallery(ctx, model.Gallery{Slug: "published", Title: "Published", Status: model.GalleryPublished})
	if err != nil {
		t.Fatal(err)
	}
	draftGallery, err := st.CreateGallery(ctx, model.Gallery{Slug: "draft", Title: "Draft", Status: model.GalleryDraft})
	if err != nil {
		t.Fatal(err)
	}
	publishedItem, err := st.CreateItem(ctx, model.Item{GalleryID: publishedGallery, OriginalPath: "published.jpg", Filename: "published.jpg", Status: model.ItemPublished})
	if err != nil {
		t.Fatal(err)
	}
	draftItem, err := st.CreateItem(ctx, model.Item{GalleryID: draftGallery, OriginalPath: "draft.jpg", Filename: "draft.jpg", Status: model.ItemPublished})
	if err != nil {
		t.Fatal(err)
	}

	if err := st.ReplaceItemUserTags(ctx, publishedItem, []string{"shared", "manual only"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceItemImportedTags(ctx, publishedItem, TagSourceMetadata, []string{"shared", "suppressed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceItemEditableTags(ctx, publishedItem, []string{"shared", "manual only"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceItemImportedTags(ctx, draftItem, TagSourceLightroom, []string{"shared", "lightroom only"}); err != nil {
		t.Fatal(err)
	}

	got, err := st.TagUsages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []TagUsage{
		{Value: "lightroom only", PhotoCount: 1, LightroomCount: 1},
		{Value: "manual only", PhotoCount: 1, PublishedCount: 1, ManualCount: 1},
		{Value: "shared", PhotoCount: 2, PublishedCount: 1, ManualCount: 1, MetadataCount: 1, LightroomCount: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("tag usages = %#v, want %#v", got, want)
	}
	for index := range want {
		want[index].ID = got[index].ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tag usages = %#v, want %#v", got, want)
	}

	items, err := st.TagUsageItems(ctx, got[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != draftItem || !items[0].Lightroom || items[0].Manual || items[0].Metadata ||
		items[1].ID != publishedItem || !items[1].Manual || !items[1].Metadata || items[1].Lightroom {
		t.Fatalf("shared tag items = %#v", items)
	}
}

func TestReplaceItemEditableTagsPersistsMetadataRemovals(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, itemIDs := makeGalleryWithItems(t, st, 1)
	itemID := itemIDs[0]

	if err := st.ReplaceItemUserTags(ctx, itemID, []string{"shared ownership"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceItemImportedTags(ctx, itemID, TagSourceMetadata, []string{"from metadata", "remove me", "shared ownership"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceItemEditableTags(ctx, itemID, []string{"from metadata", "added in curator", "shared ownership"}); err != nil {
		t.Fatal(err)
	}
	assertTagValues(t, st, ctx, itemID, []string{"added in curator", "from metadata", "shared ownership"})

	manual, err := st.ItemManualTags(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(manual) != 2 || manual[0].Value != "added in curator" || manual[1].Value != "shared ownership" {
		t.Fatalf("manual tags = %#v", manual)
	}

	if err := st.ReplaceItemImportedTags(ctx, itemID, TagSourceMetadata, []string{"from metadata", "new metadata", "shared ownership"}); err != nil {
		t.Fatal(err)
	}
	assertTagValues(t, st, ctx, itemID, []string{"added in curator", "from metadata", "new metadata", "shared ownership"})

	if err := st.ReplaceItemEditableTags(ctx, itemID, []string{"added in curator", "from metadata", "new metadata", "second curator tag", "shared ownership"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceItemImportedTags(ctx, itemID, TagSourceMetadata, []string{"from metadata", "remove me", "new metadata", "shared ownership"}); err != nil {
		t.Fatal(err)
	}
	assertTagValues(t, st, ctx, itemID, []string{"added in curator", "from metadata", "new metadata", "second curator tag", "shared ownership"})

	if err := st.ReplaceItemEditableTags(ctx, itemID, []string{"added in curator", "from metadata", "new metadata", "remove me", "second curator tag", "shared ownership"}); err != nil {
		t.Fatal(err)
	}
	assertTagValues(t, st, ctx, itemID, []string{"added in curator", "from metadata", "new metadata", "remove me", "second curator tag", "shared ownership"})
}

func assertTagValues(t *testing.T, st *Store, ctx context.Context, itemID int64, want []string) {
	t.Helper()
	tags, err := st.ItemUserTags(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(tags))
	for index, tag := range tags {
		got[index] = tag.Value
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
}

func TestUserTagMigrationMergesCanonicalVariants(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "curator.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		t.Fatal(err)
	}
	files, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.version >= 20 {
			break
		}
		if err := st.applyMigration(ctx, file); err != nil {
			t.Fatalf("migration %d: %v", file.version, err)
		}
	}

	_, itemIDs := makeGalleryWithItems(t, st, 3)
	first, err := st.DB.ExecContext(ctx, `INSERT INTO tags (namespace, value) VALUES ('user', 'Tag-Name')`)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.DB.ExecContext(ctx, `INSERT INTO tags (namespace, value) VALUES ('user', 'tag name')`)
	if err != nil {
		t.Fatal(err)
	}
	third, err := st.DB.ExecContext(ctx, `INSERT INTO tags (namespace, value) VALUES ('user', 'TAG   NAME')`)
	if err != nil {
		t.Fatal(err)
	}
	firstID, _ := first.LastInsertId()
	secondID, _ := second.LastInsertId()
	thirdID, _ := third.LastInsertId()
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO tag_map (tag_id, item_id) VALUES (?, ?), (?, ?), (?, ?)`, firstID, itemIDs[0], secondID, itemIDs[1], thirdID, itemIDs[2]); err != nil {
		t.Fatal(err)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var value string
	var tagCount, mappingCount int
	if err := st.DB.QueryRowContext(ctx, `SELECT count(*), min(value) FROM tags WHERE namespace = 'user'`).Scan(&tagCount, &value); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM tag_map JOIN tags ON tags.id = tag_map.tag_id WHERE tags.namespace = 'user'`).Scan(&mappingCount); err != nil {
		t.Fatal(err)
	}
	var source string
	if err := st.DB.QueryRowContext(ctx, `SELECT min(source) FROM tag_map`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if tagCount != 1 || value != "tag name" || mappingCount != 3 || source != "manual" {
		t.Fatalf("migrated tags: count=%d value=%q mappings=%d source=%q", tagCount, value, mappingCount, source)
	}
}
