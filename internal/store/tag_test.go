package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReplaceItemUserTagsNormalizesAndReplaces(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, itemIDs := makeGalleryWithItems(t, st, 1)
	itemID := itemIDs[0]

	if err := st.ReplaceItemUserTags(ctx, itemID, []string{" night ", "Kodak   Portra 400", "Night", ""}); err != nil {
		t.Fatal(err)
	}
	tags, err := st.ItemUserTags(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0].Value != "kodak portra 400" || tags[1].Value != "night" {
		t.Fatalf("tags = %#v, want kodak portra 400 and night", tags)
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
	if err := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM tags WHERE namespace = 'user' AND value IN ('night', 'kodak portra 400')`).Scan(&orphanCount); err != nil {
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

func TestLowercaseUserTagsMigrationMergesCaseVariants(t *testing.T) {
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
		if file.version > 20 {
			break
		}
		if err := st.applyMigration(ctx, file); err != nil {
			t.Fatalf("migration %d: %v", file.version, err)
		}
	}

	_, itemIDs := makeGalleryWithItems(t, st, 2)
	upper, err := st.DB.ExecContext(ctx, `INSERT INTO tags (namespace, value) VALUES ('user', 'Night')`)
	if err != nil {
		t.Fatal(err)
	}
	lower, err := st.DB.ExecContext(ctx, `INSERT INTO tags (namespace, value) VALUES ('user', 'night')`)
	if err != nil {
		t.Fatal(err)
	}
	upperID, _ := upper.LastInsertId()
	lowerID, _ := lower.LastInsertId()
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO tag_map (tag_id, item_id) VALUES (?, ?), (?, ?)`, upperID, itemIDs[0], lowerID, itemIDs[1]); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "metadata.tag_selection", "Night"); err != nil {
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
	settings, err := st.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tagCount != 1 || value != "night" || mappingCount != 2 || settings["metadata.tag_selection"] != "night" {
		t.Fatalf("migrated tags: count=%d value=%q mappings=%d selection=%q", tagCount, value, mappingCount, settings["metadata.tag_selection"])
	}
}
