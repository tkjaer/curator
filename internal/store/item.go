package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/tkjaer/curator/internal/model"
)

// CreateItem inserts a photo and returns its new id.
func (s *Store) CreateItem(ctx context.Context, it model.Item) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO items
			(gallery_id, original_path, filename, width, height, aspect,
			 highlighted, sort_order, status, caption, exif, camera, lens,
			 aperture, shutter, iso, focal, taken_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.GalleryID, it.OriginalPath, it.Filename, it.Width, it.Height, it.Aspect,
		it.Highlighted, it.SortOrder, it.Status, it.Caption,
		it.EXIF, it.Camera, it.Lens,
		it.Aperture, it.Shutter, it.ISO, it.Focal, timeToNull(it.TakenAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ItemsByGallery returns a gallery's items in display order: manual sort order
// first, then date taken, then filename.
func (s *Store) ItemsByGallery(ctx context.Context, galleryID int64) ([]model.Item, error) {
	var sortMode model.SortMode
	if err := s.DB.QueryRowContext(ctx,
		`SELECT sort_mode FROM galleries WHERE id = ?`, galleryID).Scan(&sortMode); err != nil {
		return nil, err
	}
	orderBy := `sort_order, taken_at IS NULL, taken_at, filename`
	if sortMode == model.SortByFilename {
		orderBy = `sort_order, filename`
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, gallery_id, original_path, filename, width, height, aspect,
		        highlighted, sort_order, status, caption, exif, camera, lens,
		        aperture, shutter, iso, focal, taken_at
		   FROM items
		  WHERE gallery_id = ?
		  ORDER BY `+orderBy, galleryID)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

// AllItems returns every item, ordered by id.
func (s *Store) AllItems(ctx context.Context) ([]model.Item, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, gallery_id, original_path, filename, width, height, aspect,
		        highlighted, sort_order, status, caption, exif, camera, lens,
		        aperture, shutter, iso, focal, taken_at
		   FROM items ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

func scanItems(rows *sql.Rows) ([]model.Item, error) {
	defer rows.Close()

	var out []model.Item
	for rows.Next() {
		var (
			it    model.Item
			taken sql.NullString
		)
		if err := rows.Scan(&it.ID, &it.GalleryID, &it.OriginalPath, &it.Filename,
			&it.Width, &it.Height, &it.Aspect, &it.Highlighted, &it.SortOrder,
			&it.Status, &it.Caption, &it.EXIF, &it.Camera, &it.Lens,
			&it.Aperture, &it.Shutter, &it.ISO, &it.Focal, &taken); err != nil {
			return nil, err
		}
		it.TakenAt = parseTime(taken)
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateItemEXIF overwrites an item's EXIF-derived fields (used by rescan).
func (s *Store) UpdateItemEXIF(ctx context.Context, it model.Item) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE items SET exif = ?, camera = ?, lens = ?, aperture = ?, shutter = ?,
		        iso = ?, focal = ?, taken_at = ?, updated_at = datetime('now')
		  WHERE id = ?`,
		it.EXIF, it.Camera, it.Lens, it.Aperture, it.Shutter, it.ISO, it.Focal,
		timeToNull(it.TakenAt), it.ID)
	return err
}

// UpsertDerivative records (or updates) a generated derivative for an item.
func (s *Store) UpsertDerivative(ctx context.Context, d model.Derivative) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO derivatives (item_id, preset, width, height, path, hash)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(item_id, preset) DO UPDATE SET
			width = excluded.width,
			height = excluded.height,
			path = excluded.path,
			hash = excluded.hash`,
		d.ItemID, d.Preset, d.Width, d.Height, d.Path, d.Hash)
	return err
}

// DerivativesByItem returns all derivatives generated for an item.
func (s *Store) DerivativesByItem(ctx context.Context, itemID int64) ([]model.Derivative, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, item_id, preset, width, height, path, hash
		   FROM derivatives WHERE item_id = ?`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Derivative
	for rows.Next() {
		var d model.Derivative
		if err := rows.Scan(&d.ID, &d.ItemID, &d.Preset, &d.Width, &d.Height, &d.Path, &d.Hash); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func timeToNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}

func parseTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s.String); err == nil {
		return &t
	}
	return nil
}
