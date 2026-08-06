package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/tkjaer/curator/internal/model"
)

const userTagNamespace = "user"

const (
	TagSourceMetadata  = "metadata"
	TagSourceLightroom = "lightroom"
	userTagSource      = "manual"
)

// ReplaceItemUserTags replaces an item's user tags with normalized values.
func (s *Store) ReplaceItemUserTags(ctx context.Context, itemID int64, values []string) error {
	return s.replaceItemTags(ctx, itemID, userTagSource, values)
}

// ReplaceItemImportedTags replaces tags assigned to an item by one import source.
func (s *Store) ReplaceItemImportedTags(ctx context.Context, itemID int64, source string, values []string) error {
	if source != TagSourceMetadata && source != TagSourceLightroom {
		return fmt.Errorf("unsupported tag source %q", source)
	}
	return s.replaceItemTags(ctx, itemID, source, values)
}

func (s *Store) replaceItemTags(ctx context.Context, itemID int64, source string, values []string) error {
	values = normalizeTagValues(values)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM tag_map WHERE item_id = ? AND source = ? AND tag_id IN (SELECT id FROM tags WHERE namespace = ?)`, itemID, source, userTagNamespace); err != nil {
		return err
	}
	for _, value := range values {
		var tagID int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE namespace = ? AND value = ? COLLATE NOCASE`, userTagNamespace, value).Scan(&tagID)
		if errors.Is(err, sql.ErrNoRows) {
			res, insertErr := tx.ExecContext(ctx, `INSERT INTO tags (namespace, value) VALUES (?, ?)`, userTagNamespace, value)
			if insertErr != nil {
				return insertErr
			}
			tagID, err = res.LastInsertId()
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tag_map (tag_id, item_id, source) VALUES (?, ?, ?)`, tagID, itemID, source); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE namespace = ? AND id NOT IN (SELECT tag_id FROM tag_map)`, userTagNamespace); err != nil {
		return err
	}
	return tx.Commit()
}

// GalleryItemUserTags returns normalized user-tag values keyed by item id.
func (s *Store) GalleryItemUserTags(ctx context.Context, galleryID int64) (map[int64][]string, error) {
	return s.galleryItemTags(ctx, galleryID, "")
}

// GalleryItemManualTags returns only tags assigned through Curator's admin.
func (s *Store) GalleryItemManualTags(ctx context.Context, galleryID int64) (map[int64][]string, error) {
	return s.galleryItemTags(ctx, galleryID, " AND tag_map.source = 'manual'")
}

// GalleryItemImportedTags returns tags assigned by metadata or external sources.
func (s *Store) GalleryItemImportedTags(ctx context.Context, galleryID int64) (map[int64][]string, error) {
	return s.galleryItemTags(ctx, galleryID, " AND tag_map.source != 'manual'")
}

func (s *Store) galleryItemTags(ctx context.Context, galleryID int64, sourceFilter string) (map[int64][]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT tag_map.item_id, tags.value
		  FROM tags
		  JOIN tag_map ON tag_map.tag_id = tags.id
		  JOIN items ON items.id = tag_map.item_id
		 WHERE items.gallery_id = ? AND tags.namespace = ?`+sourceFilter+`
		 ORDER BY tag_map.item_id, tags.value COLLATE NOCASE`, galleryID, userTagNamespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tagsByItem := make(map[int64][]string)
	for rows.Next() {
		var itemID int64
		var value string
		if err := rows.Scan(&itemID, &value); err != nil {
			return nil, err
		}
		tagsByItem[itemID] = append(tagsByItem[itemID], value)
	}
	return tagsByItem, rows.Err()
}

// ItemUserTags returns one item's user tags in case-insensitive display order.
func (s *Store) ItemUserTags(ctx context.Context, itemID int64) ([]model.Tag, error) {
	return s.itemTags(ctx, itemID, "")
}

// ItemManualTags returns only tags assigned through Curator's admin.
func (s *Store) ItemManualTags(ctx context.Context, itemID int64) ([]model.Tag, error) {
	return s.itemTags(ctx, itemID, " AND tag_map.source = 'manual'")
}

func (s *Store) itemTags(ctx context.Context, itemID int64, sourceFilter string) ([]model.Tag, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT tags.id, tags.namespace, tags.value
		  FROM tags JOIN tag_map ON tag_map.tag_id = tags.id
		 WHERE tag_map.item_id = ? AND tags.namespace = ?`+sourceFilter+`
		 ORDER BY tags.value COLLATE NOCASE`, itemID, userTagNamespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []model.Tag
	for rows.Next() {
		var tag model.Tag
		if err := rows.Scan(&tag.ID, &tag.Namespace, &tag.Value); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// UserTags returns assigned user tags in case-insensitive display order.
func (s *Store) UserTags(ctx context.Context) ([]model.Tag, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT tags.id, tags.namespace, tags.value
		  FROM tags JOIN tag_map ON tag_map.tag_id = tags.id
		 WHERE tags.namespace = ?
		 ORDER BY tags.value COLLATE NOCASE`, userTagNamespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []model.Tag
	for rows.Next() {
		var tag model.Tag
		if err := rows.Scan(&tag.ID, &tag.Namespace, &tag.Value); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func normalizeTagValues(values []string) []string {
	seen := make(map[string]bool, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ReplaceAll(value, "-", " ")
		value = strings.ToLower(strings.Join(strings.Fields(value), " "))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	return normalized
}
