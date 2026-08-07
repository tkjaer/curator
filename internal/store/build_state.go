package store

import (
	"context"
	"database/sql"
)

// BuildState records the inputs and summary of the last successful build for
// one output directory.
type BuildState struct {
	ContentRevision int64
	Fingerprint     string
	Galleries       int
	Photos          int
}

func (s *Store) BuildRevision(ctx context.Context) (int64, error) {
	var revision int64
	err := s.DB.QueryRowContext(ctx, `SELECT revision FROM build_revision WHERE id = 1`).Scan(&revision)
	return revision, err
}

func (s *Store) BuildState(ctx context.Context, outputDir string) (BuildState, bool, error) {
	var state BuildState
	err := s.DB.QueryRowContext(ctx, `
		SELECT content_revision, fingerprint, galleries, photos
		FROM build_state WHERE output_dir = ?`, outputDir).Scan(
		&state.ContentRevision, &state.Fingerprint, &state.Galleries, &state.Photos)
	if err == sql.ErrNoRows {
		return BuildState{}, false, nil
	}
	return state, err == nil, err
}

func (s *Store) SetBuildState(ctx context.Context, outputDir string, state BuildState) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO build_state (output_dir, content_revision, fingerprint, galleries, photos, built_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(output_dir) DO UPDATE SET
			content_revision = excluded.content_revision,
			fingerprint = excluded.fingerprint,
			galleries = excluded.galleries,
			photos = excluded.photos,
			built_at = excluded.built_at`,
		outputDir, state.ContentRevision, state.Fingerprint, state.Galleries, state.Photos)
	return err
}
