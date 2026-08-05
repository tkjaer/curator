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
		        cover_item_id, sort_mode, sort_direction, sort_order, theme,
		        show_exif, show_title, show_description
		   FROM galleries WHERE id = ?`, id).
		Scan(&g.ID, &parent, &g.Slug, &g.Title, &g.Description, &g.Type, &g.Status,
			&cover, &g.SortMode, &g.SortDirection, &g.SortOrder, &g.Theme,
			&g.ShowEXIF, &g.ShowTitle, &g.ShowDescription)
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

// UpdateGalleryTitle changes a gallery's display title without changing its URL.
func (s *Store) UpdateGalleryTitle(ctx context.Context, id int64, title string) error {
	if title == "" {
		return errors.New("gallery title is required")
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE galleries SET title = ?, updated_at = datetime('now') WHERE id = ?`, title, id)
	return err
}

// UpdateGallerySlug changes a gallery's public URL segment. Previous slugs are
// intentionally not retained as aliases or redirects.
func (s *Store) UpdateGallerySlug(ctx context.Context, id int64, gallerySlug string) error {
	if gallerySlug == "" {
		return errors.New("gallery slug is required")
	}
	g, err := s.Gallery(ctx, id)
	if err != nil {
		return err
	}
	if err := validateGalleryRootSlug(g.ParentID, gallerySlug); err != nil {
		return err
	}
	var exists bool
	if err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM galleries
			 WHERE parent_id IS ? AND slug = ? COLLATE NOCASE AND id <> ?
		)`, g.ParentID, gallerySlug, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errors.New("another gallery at this level already uses that slug")
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE galleries SET slug = ?, updated_at = datetime('now') WHERE id = ?`, gallerySlug, id)
	return err
}

// UpdateGalleryPresentation sets a gallery's metadata visibility overrides.
func (s *Store) UpdateGalleryPresentation(ctx context.Context, id int64, showEXIF, showTitle, showDescription model.Visibility) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE galleries
		    SET show_exif = ?, show_title = ?, show_description = ?, updated_at = datetime('now')
		  WHERE id = ?`, showEXIF, showTitle, showDescription, id)
	return err
}

// ResetGalleryPresentationOverrides makes one presentation field, or all
// fields, inherit site defaults for every gallery.
func (s *Store) ResetGalleryPresentationOverrides(ctx context.Context, field string) error {
	var query string
	switch field {
	case "title":
		query = `UPDATE galleries SET show_title = 0, updated_at = datetime('now')`
	case "description":
		query = `UPDATE galleries SET show_description = 0, updated_at = datetime('now')`
	case "exif":
		query = `UPDATE galleries SET show_exif = 0, updated_at = datetime('now')`
	case "all":
		query = `UPDATE galleries
		            SET show_exif = 0, show_title = 0, show_description = 0, updated_at = datetime('now')`
	default:
		return errors.New("invalid gallery presentation field")
	}
	_, err := s.DB.ExecContext(ctx, query)
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
		        highlighted, sort_order, status, title, description, caption, exif, camera, embedded_camera, manual_camera, lens,
		        embedded_lens, lightroom_lens, manual_lens, sidecar_lens, xmp_lens, aperture, shutter, iso, focal, taken_at
		   FROM items WHERE id = ?`, id).
		Scan(&it.ID, &it.GalleryID, &it.OriginalPath, &it.Filename, &it.Width, &it.Height,
			&it.Aspect, &it.Highlighted, &it.SortOrder, &it.Status, &it.Title, &it.Description, &it.Caption, &it.EXIF,
			&it.Camera, &it.EmbeddedCamera, &it.ManualCamera, &it.Lens, &it.EmbeddedLens, &it.LightroomLens, &it.ManualLens, &it.SidecarLens, &it.XMPLens,
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

// UpdateItemPresentation updates all editable presentation metadata.
func (s *Store) UpdateItemPresentation(ctx context.Context, id int64, title, description, caption string, status model.ItemStatus, highlighted bool, manualCamera, effectiveCamera, manualLens, effectiveLens string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE items SET title = ?, description = ?, caption = ?, status = ?, highlighted = ?, manual_camera = ?, camera = ?, manual_lens = ?, lens = ?,
		        updated_at = datetime('now') WHERE id = ?`,
		title, description, caption, status, highlighted, manualCamera, effectiveCamera, manualLens, effectiveLens, id)
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

// SetItemOrder replaces every manual photo position in a gallery atomically.
func (s *Store) SetItemOrder(ctx context.Context, galleryID int64, itemIDs []int64) error {
	items, err := s.ItemsByGallery(ctx, galleryID)
	if err != nil {
		return err
	}
	if len(itemIDs) != len(items) {
		return errors.New("item order does not match gallery")
	}

	valid := make(map[int64]bool, len(items))
	for _, item := range items {
		valid[item.ID] = true
	}
	for _, itemID := range itemIDs {
		if !valid[itemID] {
			return errors.New("item order does not match gallery")
		}
		delete(valid, itemID)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, itemID := range itemIDs {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET sort_order = ?, updated_at = datetime('now') WHERE id = ? AND gallery_id = ?`,
			i+1, itemID, galleryID); err != nil {
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

// ResetGalleryOptions restores inherited ordering and photo display settings
// and clears manual photo positions without changing gallery identity or status.
func (s *Store) ResetGalleryOptions(ctx context.Context, galleryID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE galleries
		    SET sort_mode = ?, sort_direction = ?,
		        show_exif = ?, show_title = ?, show_description = ?,
		        updated_at = datetime('now')
		  WHERE id = ?`,
		model.SortDefault, model.SortDirectionDefault,
		model.VisibilityInherit, model.VisibilityInherit, model.VisibilityInherit,
		galleryID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET sort_order = 0, updated_at = datetime('now') WHERE gallery_id = ?`,
		galleryID); err != nil {
		return err
	}
	return tx.Commit()
}
