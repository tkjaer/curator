package store

import (
	"context"

	"github.com/tkjaer/curator/internal/model"
)

// CreateAccessUser inserts a basic-auth user and returns its id.
func (s *Store) CreateAccessUser(ctx context.Context, username, hash string) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO access_users (username, hash) VALUES (?, ?)`, username, hash)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteAccessUser removes an access user (and, by cascade, its grants).
func (s *Store) DeleteAccessUser(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM access_users WHERE id = ?`, id)
	return err
}

// AccessUsers returns every access user ordered by name.
func (s *Store) AccessUsers(ctx context.Context) ([]model.AccessUser, error) {
	return s.scanAccessUsers(ctx, `SELECT id, username, hash FROM access_users ORDER BY username`)
}

// GalleryAccessUsers returns the users granted access to a gallery.
func (s *Store) GalleryAccessUsers(ctx context.Context, galleryID int64) ([]model.AccessUser, error) {
	return s.scanAccessUsers(ctx,
		`SELECT u.id, u.username, u.hash
		   FROM access_users u
		   JOIN gallery_access ga ON ga.user_id = u.id
		  WHERE ga.gallery_id = ?
		  ORDER BY u.username`, galleryID)
}

func (s *Store) scanAccessUsers(ctx context.Context, query string, args ...any) ([]model.AccessUser, error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.AccessUser
	for rows.Next() {
		var u model.AccessUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Hash); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetGalleryAccess replaces the set of users granted access to a gallery.
func (s *Store) SetGalleryAccess(ctx context.Context, galleryID int64, userIDs []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM gallery_access WHERE gallery_id = ?`, galleryID); err != nil {
		return err
	}
	for _, uid := range userIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO gallery_access (gallery_id, user_id) VALUES (?, ?)`, galleryID, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}
