package store

import (
	"context"
	"database/sql"

	"github.com/tkjaer/curator/internal/model"
)

// CreateGallery inserts a gallery and returns its new id.
func (s *Store) CreateGallery(ctx context.Context, g model.Gallery) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO galleries
			(parent_id, slug, title, description, type, status, sort_mode, sort_order)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ParentID, g.Slug, g.Title, g.Description, g.Type, g.Status, g.SortMode, g.SortOrder)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Galleries returns every gallery ordered for stable tree building.
func (s *Store) Galleries(ctx context.Context) ([]model.Gallery, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, parent_id, slug, title, description, type, status,
		        cover_item_id, sort_mode, sort_order, theme, show_exif
		   FROM galleries
		  ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Gallery
	for rows.Next() {
		var (
			g      model.Gallery
			parent sql.NullInt64
			cover  sql.NullInt64
		)
		if err := rows.Scan(&g.ID, &parent, &g.Slug, &g.Title, &g.Description,
			&g.Type, &g.Status, &cover, &g.SortMode, &g.SortOrder, &g.Theme, &g.ShowEXIF); err != nil {
			return nil, err
		}
		g.ParentID = nullInt(parent)
		g.CoverItemID = nullInt(cover)
		out = append(out, g)
	}
	return out, rows.Err()
}

func nullInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
