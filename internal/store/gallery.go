package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/tkjaer/curator/internal/model"
)

// CreateGallery inserts a gallery and returns its new id.
func (s *Store) CreateGallery(ctx context.Context, g model.Gallery) (int64, error) {
	if err := validateGalleryRootSlug(g.ParentID, g.Slug); err != nil {
		return 0, err
	}
	if g.SortMode == "" {
		g.SortMode = model.SortDefault
	}
	if g.SortDirection == "" {
		g.SortDirection = model.SortDirectionDefault
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO galleries
			(parent_id, slug, title, description, type, status, sort_mode, sort_direction, sort_order,
			 show_exif, show_title, show_description, published_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?,
			CASE WHEN ? <> 0 THEN ?
			     WHEN EXISTS (SELECT 1 FROM galleries WHERE parent_id IS ? AND sort_order <> 0)
			     THEN (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM galleries WHERE parent_id IS ?)
			     ELSE 0 END,
			?, ?, ?, CASE WHEN ? = 'published' THEN datetime('now') END)`,
		g.ParentID, g.Slug, g.Title, g.Description, g.Type, g.Status, g.SortMode, g.SortDirection,
		g.SortOrder, g.SortOrder, g.ParentID, g.ParentID,
		g.ShowEXIF, g.ShowTitle, g.ShowDescription, g.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func validateGalleryRootSlug(parentID *int64, gallerySlug string) error {
	if parentID != nil {
		return nil
	}
	for _, reserved := range []string{"_curator", "browse", "feed.xml"} {
		if strings.EqualFold(gallerySlug, reserved) {
			return fmt.Errorf("gallery slug %q is reserved at the site root", gallerySlug)
		}
	}
	return nil
}

// Galleries returns every gallery ordered for stable tree building.
func (s *Store) Galleries(ctx context.Context) ([]model.Gallery, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, parent_id, slug, title, description, type, status,
		        cover_item_id, sort_mode, sort_direction, sort_order, theme,
		        show_exif, show_title, show_description, published_at
		   FROM galleries
		  ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Gallery
	for rows.Next() {
		var (
			g         model.Gallery
			parent    sql.NullInt64
			cover     sql.NullInt64
			published sql.NullString
		)
		if err := rows.Scan(&g.ID, &parent, &g.Slug, &g.Title, &g.Description,
			&g.Type, &g.Status, &cover, &g.SortMode, &g.SortDirection, &g.SortOrder, &g.Theme,
			&g.ShowEXIF, &g.ShowTitle, &g.ShowDescription, &published); err != nil {
			return nil, err
		}
		g.ParentID = nullInt(parent)
		g.CoverItemID = nullInt(cover)
		g.PublishedAt = nullTime(published)
		out = append(out, g)
	}
	return out, rows.Err()
}

// MoveGalleryOrder moves a gallery one position among its siblings.
func (s *Store) MoveGalleryOrder(ctx context.Context, galleryID int64, earlier bool) error {
	galleries, err := s.Galleries(ctx)
	if err != nil {
		return err
	}
	var parentID *int64
	found := false
	for _, gallery := range galleries {
		if gallery.ID == galleryID {
			parentID = gallery.ParentID
			found = true
			break
		}
	}
	if !found {
		return sql.ErrNoRows
	}

	siblings := make([]model.Gallery, 0)
	position := -1
	for _, gallery := range galleries {
		if !sameParent(gallery.ParentID, parentID) {
			continue
		}
		if gallery.ID == galleryID {
			position = len(siblings)
		}
		siblings = append(siblings, gallery)
	}
	target := position + 1
	if earlier {
		target = position - 1
	}
	if position < 0 || target < 0 || target >= len(siblings) {
		return nil
	}
	siblings[position], siblings[target] = siblings[target], siblings[position]

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for index, gallery := range siblings {
		if _, err := tx.ExecContext(ctx,
			`UPDATE galleries SET sort_order = ?, updated_at = datetime('now') WHERE id = ?`, index+1, gallery.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func sameParent(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func nullInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// nullTime parses a SQLite datetime string (UTC) into a *time.Time.
func nullTime(n sql.NullString) *time.Time {
	if !n.Valid || n.String == "" {
		return nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", n.String); err == nil {
		u := t.UTC()
		return &u
	}
	if t, err := time.Parse(time.RFC3339, n.String); err == nil {
		u := t.UTC()
		return &u
	}
	return nil
}
