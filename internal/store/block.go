package store

import (
	"context"
	"database/sql"

	"github.com/tkjaer/curator/internal/model"
)

// CreateBlock appends a block to a gallery and returns its id.
func (s *Store) CreateBlock(ctx context.Context, b model.Block) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO blocks (gallery_id, type, item_id, content, sort_order)
		 VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM blocks WHERE gallery_id = ?))`,
		b.GalleryID, b.Type, b.ItemID, b.Content, b.GalleryID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// BlocksByGallery returns a gallery's blocks in order.
func (s *Store) BlocksByGallery(ctx context.Context, galleryID int64) ([]model.Block, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, gallery_id, type, item_id, content, sort_order
		   FROM blocks WHERE gallery_id = ? ORDER BY sort_order, id`, galleryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Block
	for rows.Next() {
		var (
			b    model.Block
			item sql.NullInt64
		)
		if err := rows.Scan(&b.ID, &b.GalleryID, &b.Type, &item, &b.Content, &b.SortOrder); err != nil {
			return nil, err
		}
		b.ItemID = nullInt(item)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Block returns a single block by id.
func (s *Store) Block(ctx context.Context, id int64) (model.Block, error) {
	var (
		b    model.Block
		item sql.NullInt64
	)
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, gallery_id, type, item_id, content, sort_order FROM blocks WHERE id = ?`, id).
		Scan(&b.ID, &b.GalleryID, &b.Type, &item, &b.Content, &b.SortOrder)
	if err != nil {
		return model.Block{}, err
	}
	b.ItemID = nullInt(item)
	return b, nil
}

// UpdateBlock sets a block's content and (for image blocks) its item.
func (s *Store) UpdateBlock(ctx context.Context, id int64, content string, itemID *int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE blocks SET content = ?, item_id = ? WHERE id = ?`, content, itemID, id)
	return err
}

// DeleteBlock removes a block (and its block_items by cascade).
func (s *Store) DeleteBlock(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM blocks WHERE id = ?`, id)
	return err
}

// MoveBlock shifts a block one position earlier or later, normalizing order.
func (s *Store) MoveBlock(ctx context.Context, galleryID, blockID int64, up bool) error {
	blocks, err := s.BlocksByGallery(ctx, galleryID)
	if err != nil {
		return err
	}
	pos := -1
	for i, bl := range blocks {
		if bl.ID == blockID {
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
	if swap < 0 || swap >= len(blocks) {
		return nil
	}
	blocks[pos], blocks[swap] = blocks[swap], blocks[pos]

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, bl := range blocks {
		if _, err := tx.ExecContext(ctx,
			`UPDATE blocks SET sort_order = ? WHERE id = ?`, i+1, bl.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetBlockItems replaces the ordered set of items shown by a grid block.
func (s *Store) SetBlockItems(ctx context.Context, blockID int64, itemIDs []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM block_items WHERE block_id = ?`, blockID); err != nil {
		return err
	}
	for i, itemID := range itemIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO block_items (block_id, item_id, sort_order) VALUES (?, ?, ?)`,
			blockID, itemID, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BlockItemIDs returns the item ids of a grid block, in order.
func (s *Store) BlockItemIDs(ctx context.Context, blockID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT item_id FROM block_items WHERE block_id = ? ORDER BY sort_order`, blockID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
