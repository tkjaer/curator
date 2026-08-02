package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/tkjaer/curator/internal/model"
)

// Settings returns all settings with their JSON values decoded to strings.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = decodeSetting(value)
	}
	return out, rows.Err()
}

// DefaultGallerySortMode returns the system ordering inherited by galleries
// whose sort mode is default.
func (s *Store) DefaultGallerySortMode(ctx context.Context) (model.SortMode, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = 'site.default_gallery_order'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return model.SortByDate, nil
	}
	if err != nil {
		return "", err
	}
	if mode := model.SortMode(decodeSetting(raw)); mode == model.SortByFilename {
		return mode, nil
	}
	return model.SortByDate, nil
}

// EffectiveGallerySortMode resolves a gallery's inherited ordering.
func (s *Store) EffectiveGallerySortMode(ctx context.Context, mode model.SortMode) (model.SortMode, error) {
	if mode == model.SortDefault || mode == "" {
		return s.DefaultGallerySortMode(ctx)
	}
	return mode, nil
}

// GalleryDefaults returns the initial visibility and EXIF presentation for new
// galleries. Missing settings retain the conservative draft/off behavior.
func (s *Store) GalleryDefaults(ctx context.Context) (model.GalleryStatus, bool, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return "", false, err
	}
	status := model.GalleryDraft
	if settings["site.default_gallery_published"] == "true" {
		status = model.GalleryPublished
	}
	return status, settings["site.default_gallery_show_exif"] == "true", nil
}

func decodeSetting(raw string) string {
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		return s
	}
	return raw
}

// Presets returns the configured derivative presets.
func (s *Store) Presets(ctx context.Context) ([]model.Preset, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT name, kind, max_width, max_height, quality FROM derivative_presets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Preset
	for rows.Next() {
		var p model.Preset
		if err := rows.Scan(&p.Name, &p.Kind, &p.MaxWidth, &p.MaxHeight, &p.Quality); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FacetConfigs returns all facet configurations ordered by namespace.
func (s *Store) FacetConfigs(ctx context.Context) ([]model.FacetConfig, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT namespace, enabled, source, label FROM facet_config ORDER BY namespace`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.FacetConfig
	for rows.Next() {
		var f model.FacetConfig
		if err := rows.Scan(&f.Namespace, &f.Enabled, &f.Source, &f.Label); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
