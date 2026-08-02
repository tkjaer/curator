package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/tkjaer/curator/internal/model"
)

// UpsertExternalGallery creates or updates a gallery owned by an external
// publisher and returns its stable Curator id.
func (s *Store) UpsertExternalGallery(ctx context.Context, source, externalID string, gallery model.Gallery) (int64, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(ctx,
		`SELECT gallery_id FROM external_galleries WHERE source = ? AND external_id = ?`,
		source, externalID).Scan(&id)
	created := err == sql.ErrNoRows
	if err != nil && !created {
		return 0, false, err
	}
	if created {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO galleries
				(parent_id, slug, title, description, type, status, sort_mode, sort_order, show_exif, published_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CASE WHEN ? = 'published' THEN datetime('now') END)`,
			gallery.ParentID, gallery.Slug, gallery.Title, gallery.Description, gallery.Type,
			gallery.Status, gallery.SortMode, gallery.SortOrder, gallery.ShowEXIF, gallery.Status)
		if err != nil {
			return 0, false, err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return 0, false, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO external_galleries (source, external_id, gallery_id) VALUES (?, ?, ?)`,
			source, externalID, id); err != nil {
			return 0, false, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE galleries
			SET parent_id = ?, slug = ?, title = ?, description = ?,
				updated_at = datetime('now')
			WHERE id = ?`,
			gallery.ParentID, gallery.Slug, gallery.Title, gallery.Description, id); err != nil {
			return 0, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, created, nil
}

func (s *Store) SetExternalGallery(ctx context.Context, source, externalID string, galleryID int64) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO external_galleries (source, external_id, gallery_id)
		VALUES (?, ?, ?)
		ON CONFLICT (source, external_id) DO UPDATE SET gallery_id = excluded.gallery_id`,
		source, externalID, galleryID)
	return err
}

func (s *Store) ExternalGalleryID(ctx context.Context, source, externalID string) (int64, bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT gallery_id FROM external_galleries WHERE source = ? AND external_id = ?`,
		source, externalID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return id, err == nil, err
}

func (s *Store) SetExternalItem(ctx context.Context, source, externalID string, itemID int64) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO external_items (source, external_id, item_id)
		VALUES (?, ?, ?)
		ON CONFLICT (source, external_id) DO UPDATE SET item_id = excluded.item_id`,
		source, externalID, itemID)
	return err
}

func (s *Store) ExternalItemID(ctx context.Context, source, externalID string) (int64, bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT item_id FROM external_items WHERE source = ? AND external_id = ?`,
		source, externalID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return id, err == nil, err
}

func (s *Store) IsExternalItem(ctx context.Context, source string, itemID int64) (bool, error) {
	var exists bool
	err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM external_items WHERE source = ? AND item_id = ?)`,
		source, itemID).Scan(&exists)
	return exists, err
}

func (s *Store) IsExternalGallery(ctx context.Context, source string, galleryID int64) (bool, error) {
	var exists bool
	err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM external_galleries WHERE source = ? AND gallery_id = ?)`,
		source, galleryID).Scan(&exists)
	return exists, err
}

// ExternalGalleryTreeOwned reports whether every gallery and item that would
// cascade from deleting galleryID is owned by the same external publisher.
func (s *Store) ExternalGalleryTreeOwned(ctx context.Context, source string, galleryID int64) (bool, error) {
	var owned bool
	err := s.DB.QueryRowContext(ctx, `
		WITH RECURSIVE tree(id) AS (
			SELECT id FROM galleries WHERE id = ?
			UNION ALL
			SELECT galleries.id FROM galleries JOIN tree ON galleries.parent_id = tree.id
		)
		SELECT NOT EXISTS (
			SELECT 1 FROM tree
			WHERE NOT EXISTS (
				SELECT 1 FROM external_galleries
				WHERE source = ? AND gallery_id = tree.id
			)
			UNION ALL
			SELECT 1 FROM items JOIN tree ON tree.id = items.gallery_id
			WHERE NOT EXISTS (
				SELECT 1 FROM external_items
				WHERE source = ? AND item_id = items.id
			)
		)`, galleryID, source, source).Scan(&owned)
	return owned, err
}

func (s *Store) SetExternalItemOrder(ctx context.Context, source string, galleryID int64, itemIDs []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE galleries SET sort_mode = ?, updated_at = datetime('now') WHERE id = ?`,
		model.SortManual, galleryID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE items SET sort_order = ? WHERE gallery_id = ?`, len(itemIDs)+1, galleryID); err != nil {
		return err
	}
	for index, itemID := range itemIDs {
		result, err := tx.ExecContext(ctx, `
			UPDATE items SET sort_order = ?, updated_at = datetime('now')
			WHERE id = ? AND gallery_id = ?
			  AND EXISTS (SELECT 1 FROM external_items WHERE source = ? AND item_id = items.id)`,
			index+1, itemID, galleryID, source)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf("item %d is not synchronized to gallery %d", itemID, galleryID)
		}
	}
	return tx.Commit()
}
