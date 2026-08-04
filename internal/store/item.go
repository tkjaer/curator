package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/tkjaer/curator/internal/model"
)

// CreateItem inserts a photo and returns its new id.
func (s *Store) CreateItem(ctx context.Context, it model.Item) (int64, error) {
	normalizeItemCamera(&it)
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO items
			(gallery_id, original_path, filename, width, height, aspect,
			 highlighted, sort_order, status, title, description, caption, exif, camera, embedded_camera, manual_camera, lens,
			 embedded_lens, lightroom_lens, manual_lens, sidecar_lens, xmp_lens, aperture, shutter, iso, focal, taken_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.GalleryID, it.OriginalPath, it.Filename, it.Width, it.Height, it.Aspect,
		it.Highlighted, it.SortOrder, it.Status, it.Title, it.Description, it.Caption,
		it.EXIF, it.Camera, it.EmbeddedCamera, it.ManualCamera, it.Lens, it.EmbeddedLens, it.LightroomLens, it.ManualLens, it.SidecarLens, it.XMPLens,
		it.Aperture, it.Shutter, it.ISO, it.Focal, timeToNull(it.TakenAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ReplaceItemMedia updates an item's source image metadata and invalidates all
// generated derivatives while preserving its stable id and presentation state.
func (s *Store) ReplaceItemMedia(ctx context.Context, it model.Item) error {
	normalizeItemCamera(&it)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE items SET gallery_id = ?, original_path = ?, filename = ?, width = ?, height = ?, aspect = ?,
			exif = ?, camera = ?, embedded_camera = ?, manual_camera = ?, lens = ?, embedded_lens = ?, lightroom_lens = ?, manual_lens = ?, sidecar_lens = ?, xmp_lens = ?,
			aperture = ?, shutter = ?, iso = ?, focal = ?, taken_at = ?, updated_at = datetime('now')
		WHERE id = ?`,
		it.GalleryID, it.OriginalPath, it.Filename, it.Width, it.Height, it.Aspect,
		it.EXIF, it.Camera, it.EmbeddedCamera, it.ManualCamera, it.Lens, it.EmbeddedLens, it.LightroomLens, it.ManualLens, it.SidecarLens, it.XMPLens,
		it.Aperture, it.Shutter, it.ISO, it.Focal, timeToNull(it.TakenAt), it.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM derivatives WHERE item_id = ?`, it.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// ItemsByGallery returns a gallery's items in display order: manual sort order
// first, then date taken, then filename.
func (s *Store) ItemsByGallery(ctx context.Context, galleryID int64) ([]model.Item, error) {
	var sortMode model.SortMode
	var sortDirection model.SortDirection
	if err := s.DB.QueryRowContext(ctx,
		`SELECT sort_mode, sort_direction FROM galleries WHERE id = ?`, galleryID).Scan(&sortMode, &sortDirection); err != nil {
		return nil, err
	}
	sortMode, err := s.EffectiveGallerySortMode(ctx, sortMode)
	if err != nil {
		return nil, err
	}
	sortDirection, err = s.EffectiveGallerySortDirection(ctx, sortDirection)
	if err != nil {
		return nil, err
	}
	orderBy := `sort_order, taken_at IS NULL, taken_at, filename`
	if sortMode == model.SortByDateAdded {
		orderBy = `sort_order, created_at, id`
	} else if sortMode == model.SortByFilename {
		orderBy = `sort_order, filename`
	}
	if sortDirection == model.SortDescending {
		if sortMode == model.SortByDateAdded {
			orderBy = `sort_order, created_at DESC, id DESC`
		} else if sortMode == model.SortByFilename {
			orderBy = `sort_order, filename DESC`
		} else {
			orderBy = `sort_order, taken_at IS NULL, taken_at DESC, filename DESC`
		}
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, gallery_id, original_path, filename, width, height, aspect,
		        highlighted, sort_order, status, title, description, caption, exif, camera, embedded_camera, manual_camera, lens,
		        embedded_lens, lightroom_lens, manual_lens, sidecar_lens, xmp_lens, aperture, shutter, iso, focal, taken_at
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
		        highlighted, sort_order, status, title, description, caption, exif, camera, embedded_camera, manual_camera, lens,
		        embedded_lens, lightroom_lens, manual_lens, sidecar_lens, xmp_lens, aperture, shutter, iso, focal, taken_at
		   FROM items ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

// ItemsInGalleryTree returns items owned by a gallery and all descendants.
func (s *Store) ItemsInGalleryTree(ctx context.Context, galleryID int64) ([]model.Item, error) {
	rows, err := s.DB.QueryContext(ctx, `
		WITH RECURSIVE tree(id) AS (
			SELECT id FROM galleries WHERE id = ?
			UNION ALL
			SELECT galleries.id FROM galleries JOIN tree ON galleries.parent_id = tree.id
		)
		SELECT id, gallery_id, original_path, filename, width, height, aspect,
		       highlighted, sort_order, status, title, description, caption, exif, camera, embedded_camera, manual_camera, lens,
		       embedded_lens, lightroom_lens, manual_lens, sidecar_lens, xmp_lens, aperture, shutter, iso, focal, taken_at
		  FROM items WHERE gallery_id IN (SELECT id FROM tree) ORDER BY id`, galleryID)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

// XMPProfileUsage is the number of photos carrying one XMP lens profile for a
// camera. Rows with the same profile can be combined for display.
type XMPProfileUsage struct {
	Profile      string
	Camera       string
	Count        int
	SidecarCount int
}

// CameraLensClue groups lens-identification evidence for one camera.
type CameraLensClue struct {
	Camera          string
	Focal           string
	MaxApertureAPEX string
	XMPProfile      string
	Count           int
}

// LensSuggestion is an existing effective lens name that can be reused as a
// manual override. Explicit override usage ranks ahead of total usage.
type LensSuggestion struct {
	Name        string
	Count       int
	ManualCount int
}

// CameraSuggestion is an existing effective camera name that can be reused as
// a manual override. Explicit override usage ranks ahead of total usage.
type CameraSuggestion struct {
	Name        string
	Count       int
	ManualCount int
}

// CameraSuggestions returns existing effective camera names ordered by how
// useful they are likely to be when assigning another photo.
func (s *Store) CameraSuggestions(ctx context.Context) ([]CameraSuggestion, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT trim(camera), count(*), sum(CASE WHEN trim(manual_camera) <> '' THEN 1 ELSE 0 END)
		   FROM items
		  WHERE trim(camera) <> ''
		  GROUP BY trim(camera)
		  ORDER BY 3 DESC, 2 DESC, trim(camera) COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var suggestions []CameraSuggestion
	for rows.Next() {
		var suggestion CameraSuggestion
		if err := rows.Scan(&suggestion.Name, &suggestion.Count, &suggestion.ManualCount); err != nil {
			return nil, err
		}
		suggestions = append(suggestions, suggestion)
	}
	return suggestions, rows.Err()
}

// LensSuggestions returns existing effective lens names ordered by how useful
// they are likely to be when assigning another photo.
func (s *Store) LensSuggestions(ctx context.Context) ([]LensSuggestion, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT trim(lens), count(*), sum(CASE WHEN trim(manual_lens) <> '' THEN 1 ELSE 0 END)
		   FROM items
		  WHERE trim(lens) <> ''
		  GROUP BY trim(lens)
		  ORDER BY 3 DESC, 2 DESC, trim(lens) COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var suggestions []LensSuggestion
	for rows.Next() {
		var suggestion LensSuggestion
		if err := rows.Scan(&suggestion.Name, &suggestion.Count, &suggestion.ManualCount); err != nil {
			return nil, err
		}
		suggestions = append(suggestions, suggestion)
	}
	return suggestions, rows.Err()
}

// CameraLensClues returns metadata combinations for photos without an embedded
// lens name. MaxApertureAPEX is retained as a rational for conversion by the UI.
func (s *Store) CameraLensClues(ctx context.Context) ([]CameraLensClue, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT trim(camera), trim(focal),
		        CASE WHEN json_valid(exif)
		             THEN COALESCE(json_extract(exif, '$.MaxApertureValue[0]'), '')
		             ELSE '' END,
		        trim(xmp_lens), count(*)
		   FROM items
		  WHERE trim(camera) <> '' AND trim(embedded_lens) = '' AND trim(sidecar_lens) = ''
		  GROUP BY trim(camera), trim(focal),
		           CASE WHEN json_valid(exif)
		                THEN COALESCE(json_extract(exif, '$.MaxApertureValue[0]'), '')
		                ELSE '' END,
		           trim(xmp_lens)
		  ORDER BY trim(camera) COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clues []CameraLensClue
	for rows.Next() {
		var clue CameraLensClue
		if err := rows.Scan(&clue.Camera, &clue.Focal, &clue.MaxApertureAPEX, &clue.XMPProfile, &clue.Count); err != nil {
			return nil, err
		}
		clues = append(clues, clue)
	}
	return clues, rows.Err()
}

// XMPProfileUsages returns stored Lightroom lens profiles grouped by camera.
func (s *Store) XMPProfileUsages(ctx context.Context) ([]XMPProfileUsage, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT trim(xmp_lens), trim(camera), count(*),
		        sum(CASE WHEN trim(sidecar_lens) <> '' THEN 1 ELSE 0 END)
		   FROM items
		  WHERE trim(xmp_lens) <> ''
		  GROUP BY trim(xmp_lens), trim(camera)
		  ORDER BY trim(xmp_lens) COLLATE NOCASE, trim(camera) COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var usages []XMPProfileUsage
	for rows.Next() {
		var usage XMPProfileUsage
		if err := rows.Scan(&usage.Profile, &usage.Camera, &usage.Count, &usage.SidecarCount); err != nil {
			return nil, err
		}
		usages = append(usages, usage)
	}
	return usages, rows.Err()
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
			&it.Status, &it.Title, &it.Description, &it.Caption, &it.EXIF, &it.Camera, &it.EmbeddedCamera, &it.ManualCamera, &it.Lens,
			&it.EmbeddedLens, &it.LightroomLens, &it.ManualLens, &it.SidecarLens, &it.XMPLens,
			&it.Aperture, &it.Shutter, &it.ISO, &it.Focal, &taken); err != nil {
			return nil, err
		}
		it.TakenAt = parseTime(taken)
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetItemLightroomLens stores the explicit lens assigned through Lightroom's
// Curator Lens keyword hierarchy and updates the current effective value.
func (s *Store) SetItemLightroomLens(ctx context.Context, id int64, source, effective string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE items SET lightroom_lens = ?, lens = ?, updated_at = datetime('now') WHERE id = ?`,
		source, effective, id)
	return err
}

// UpdateItemEXIF overwrites an item's EXIF-derived fields (used by rescan).
func (s *Store) UpdateItemEXIF(ctx context.Context, it model.Item) error {
	normalizeItemCamera(&it)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE items SET exif = ?, camera = ?, embedded_camera = ?, manual_camera = ?, lens = ?, embedded_lens = ?, sidecar_lens = ?, xmp_lens = ?, aperture = ?, shutter = ?,
		        iso = ?, focal = ?, taken_at = ?, updated_at = datetime('now')
		  WHERE id = ?`,
		it.EXIF, it.Camera, it.EmbeddedCamera, it.ManualCamera, it.Lens, it.EmbeddedLens, it.SidecarLens, it.XMPLens, it.Aperture, it.Shutter, it.ISO, it.Focal,
		timeToNull(it.TakenAt), it.ID)
	return err
}

func normalizeItemCamera(it *model.Item) {
	if it.EmbeddedCamera == "" && it.ManualCamera == "" {
		it.EmbeddedCamera = it.Camera
	}
	if it.ManualCamera != "" {
		it.Camera = it.ManualCamera
	} else if it.Camera == "" {
		it.Camera = it.EmbeddedCamera
	}
}

// FillItemTextMetadata initializes empty text fields without overwriting edits.
func (s *Store) FillItemTextMetadata(ctx context.Context, id int64, title, description string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE items SET
			title = CASE WHEN trim(title) = '' THEN ? ELSE title END,
			description = CASE WHEN trim(description) = '' THEN ? ELSE description END,
			updated_at = datetime('now')
		 WHERE id = ?`, title, description, id)
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
