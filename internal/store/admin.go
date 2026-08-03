package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/tkjaer/curator/internal/model"
)

// Gallery returns a single gallery by id.
func (s *Store) Gallery(ctx context.Context, id int64) (model.Gallery, error) {
	var (
		g      model.Gallery
		parent sql.NullInt64
		cover  sql.NullInt64
	)
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, parent_id, slug, title, description, type, status,
		        cover_item_id, sort_mode, sort_direction, sort_order, theme, show_exif
		   FROM galleries WHERE id = ?`, id).
		Scan(&g.ID, &parent, &g.Slug, &g.Title, &g.Description, &g.Type, &g.Status,
			&cover, &g.SortMode, &g.SortDirection, &g.SortOrder, &g.Theme, &g.ShowEXIF)
	if err != nil {
		return model.Gallery{}, err
	}
	g.ParentID = nullInt(parent)
	g.CoverItemID = nullInt(cover)
	return g, nil
}

// UpdateGalleryStatus sets a gallery's publication status. The first time a
// gallery becomes published it stamps published_at, preserving that date on
// later status changes.
func (s *Store) UpdateGalleryStatus(ctx context.Context, id int64, status model.GalleryStatus) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE galleries
		    SET status = ?,
		        published_at = CASE
		            WHEN ? = 'published' AND published_at IS NULL THEN datetime('now')
		            ELSE published_at
		        END,
		        updated_at = datetime('now')
		  WHERE id = ?`, status, status, id)
	return err
}

// UpdateGalleryShowEXIF toggles camera metadata in a gallery's lightbox.
func (s *Store) UpdateGalleryShowEXIF(ctx context.Context, id int64, show bool) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE galleries SET show_exif = ?, updated_at = datetime('now') WHERE id = ?`, show, id)
	return err
}

// SetSetting stores a setting value, encoded as a JSON string.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(encoded))
	return err
}

// SetFacetEnabled enables or disables a browse facet.
func (s *Store) SetFacetEnabled(ctx context.Context, namespace string, enabled bool) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE facet_config SET enabled = ? WHERE namespace = ?`, enabled, namespace)
	return err
}

// CountItems returns the number of items in a gallery.
func (s *Store) CountItems(ctx context.Context, galleryID int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM items WHERE gallery_id = ?`, galleryID).Scan(&n)
	return n, err
}

// CountPublishedItems returns the number of published items in galleries that
// are built (published, protected, or unlisted). Used for build progress totals.
func (s *Store) CountPublishedItems(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*)
		   FROM items i JOIN galleries g ON g.id = i.gallery_id
		  WHERE i.status = 'published'
		    AND g.status IN ('published', 'protected', 'unlisted')`).Scan(&n)
	return n, err
}

// DeleteGallery removes a gallery and, by cascade, its sub-galleries and items.
func (s *Store) DeleteGallery(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM galleries WHERE id = ?`, id)
	return err
}

// MoveGallery reparents a gallery, rejecting moves that would create a cycle
// (into itself or one of its own descendants). A nil parent moves it to the top
// level.
func (s *Store) MoveGallery(ctx context.Context, id int64, parent *int64) error {
	g, err := s.Gallery(ctx, id)
	if err != nil {
		return err
	}
	if err := validateGalleryRootSlug(parent, g.Slug); err != nil {
		return err
	}
	if parent != nil {
		if *parent == id {
			return errors.New("a gallery cannot be its own parent")
		}
		// Walk up from the target parent; reaching id means id is an ancestor of
		// the target, so the move would create a cycle.
		cur := parent
		for cur != nil {
			g, err := s.Gallery(ctx, *cur)
			if err != nil {
				return err
			}
			if g.ID == id {
				return errors.New("cannot move a gallery into one of its own sub-galleries")
			}
			cur = g.ParentID
		}
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE galleries SET parent_id = ?, updated_at = datetime('now') WHERE id = ?`, parent, id)
	return err
}

// Item returns a single item by id.
func (s *Store) Item(ctx context.Context, id int64) (model.Item, error) {
	var (
		it    model.Item
		taken sql.NullString
	)
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, gallery_id, original_path, filename, width, height, aspect,
		        highlighted, sort_order, status, caption, exif, camera, lens,
		        embedded_lens, lightroom_lens, sidecar_lens, xmp_lens, aperture, shutter, iso, focal, taken_at
		   FROM items WHERE id = ?`, id).
		Scan(&it.ID, &it.GalleryID, &it.OriginalPath, &it.Filename, &it.Width, &it.Height,
			&it.Aspect, &it.Highlighted, &it.SortOrder, &it.Status, &it.Caption, &it.EXIF,
			&it.Camera, &it.Lens, &it.EmbeddedLens, &it.LightroomLens, &it.SidecarLens, &it.XMPLens,
			&it.Aperture, &it.Shutter, &it.ISO, &it.Focal, &taken)
	if err != nil {
		return model.Item{}, err
	}
	it.TakenAt = parseTime(taken)
	return it, nil
}

// UpdateItemFields updates an item's editable presentation fields.
func (s *Store) UpdateItemFields(ctx context.Context, id int64, caption string, status model.ItemStatus, highlighted bool) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE items SET caption = ?, status = ?, highlighted = ?,
		        updated_at = datetime('now') WHERE id = ?`,
		caption, status, highlighted, id)
	return err
}

// SetGalleryCover sets (or clears, when itemID is nil) a gallery's cover image.
func (s *Store) SetGalleryCover(ctx context.Context, galleryID int64, itemID *int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE galleries SET cover_item_id = ?, updated_at = datetime('now') WHERE id = ?`,
		itemID, galleryID)
	return err
}

// DeleteItem removes an item (cascading its derivatives) and clears it as a
// cover if any gallery referenced it.
func (s *Store) DeleteItem(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE galleries SET cover_item_id = NULL WHERE cover_item_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// MoveItem shifts an item one position earlier or later within its gallery,
// normalizing every item's sort order so the new arrangement sticks.
func (s *Store) MoveItem(ctx context.Context, galleryID, itemID int64, up bool) error {
	items, err := s.ItemsByGallery(ctx, galleryID)
	if err != nil {
		return err
	}

	pos := -1
	for i, it := range items {
		if it.ID == itemID {
			pos = i
			break
		}
	}
	if pos == -1 {
		return nil
	}
	swap := pos + 1
	if up {
		swap = pos - 1
	}
	if swap < 0 || swap >= len(items) {
		return nil
	}
	items[pos], items[swap] = items[swap], items[pos]

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, it := range items {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET sort_order = ? WHERE id = ?`, i+1, it.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetGalleryItemOrder selects the gallery's automatic ordering and clears any
// manual photo positions.
func (s *Store) SetGalleryItemOrder(ctx context.Context, galleryID int64, mode model.SortMode, direction model.SortDirection) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE galleries SET sort_mode = ?, sort_direction = ?, updated_at = datetime('now') WHERE id = ?`,
		mode, direction, galleryID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET sort_order = 0, updated_at = datetime('now') WHERE gallery_id = ?`,
		galleryID); err != nil {
		return err
	}
	return tx.Commit()
}
