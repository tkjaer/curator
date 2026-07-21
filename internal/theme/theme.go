// Package theme loads a theme directory: its manifest and templates. Themes are
// plain data (templates, CSS, JS) and receive render view models only.
package theme

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
)

// Option is a theme setting declared in the manifest. The admin renders these
// as form fields.
type Option struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Label   string `json:"label"`
	Default any    `json:"default"`
}

// Manifest is a theme's metadata and declared options.
type Manifest struct {
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Engine          string   `json:"engine"`
	RequiresPresets []string `json:"requiresPresets"`
	Options         []Option `json:"options"`
}

// Theme is a loaded, ready-to-render theme.
type Theme struct {
	Manifest  Manifest
	fsys      fs.FS
	templates *template.Template
}

// Load reads a theme rooted at fsys: manifest.json plus templates/*.html and
// templates/partials/*.html.
func Load(fsys fs.FS) (*Theme, error) {
	manifestBytes, err := fs.ReadFile(fsys, "manifest.json")
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	tmpl := template.New(m.Name)
	patterns := []string{"templates/*.html", "templates/partials/*.html"}
	for _, p := range patterns {
		if matches, _ := fs.Glob(fsys, p); len(matches) > 0 {
			if _, err := tmpl.ParseFS(fsys, p); err != nil {
				return nil, fmt.Errorf("parse %s: %w", p, err)
			}
		}
	}

	return &Theme{Manifest: m, fsys: fsys, templates: tmpl}, nil
}

// Render executes the named template with data.
func (t *Theme) Render(w io.Writer, name string, data any) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

// Assets returns the theme's assets directory as a filesystem.
func (t *Theme) Assets() (fs.FS, error) {
	return fs.Sub(t.fsys, "assets")
}

// Defaults returns the manifest's option defaults keyed by option key.
func (m Manifest) Defaults() map[string]any {
	out := make(map[string]any, len(m.Options))
	for _, o := range m.Options {
		out[o.Key] = o.Default
	}
	return out
}
