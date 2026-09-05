// Package theme loads a theme directory: its manifest and templates. Themes are
// plain data (templates, CSS, JS) and receive render view models only.
package theme

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"testing/fstest"
)

//go:embed shared/templates/*.html shared/assets/*
var sharedFS embed.FS

// Option is a theme setting declared in the manifest. The admin renders these
// as form fields.
type Option struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Label   string `json:"label"`
	Default any    `json:"default"`
	Min     *int   `json:"min,omitempty"`
	Max     *int   `json:"max,omitempty"`
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
	assets    fstest.MapFS
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
	themeTemplates := map[string]*template.Template{}
	patterns := []string{"templates/*.html", "templates/partials/*.html"}
	for _, pattern := range patterns {
		matches, _ := fs.Glob(fsys, pattern)
		for _, match := range matches {
			parsed, err := template.New(path.Base(match)).ParseFS(fsys, match)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", match, err)
			}
			for _, override := range parsed.Templates() {
				if override.Tree != nil {
					themeTemplates[override.Name()] = override
				}
			}
		}
	}
	for _, empty := range []bool{true, false} {
		for name, override := range themeTemplates {
			isEmpty := override.Tree.Root == nil || len(override.Tree.Root.Nodes) == 0
			if isEmpty != empty {
				continue
			}
			if _, err := tmpl.New(name).AddParseTree(name, override.Tree); err != nil {
				return nil, fmt.Errorf("apply template override %s: %w", name, err)
			}
		}
	}

	sharedTemplates, err := template.New(m.Name).ParseFS(sharedFS, "shared/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse shared templates: %w", err)
	}
	for _, shared := range sharedTemplates.Templates() {
		existing := tmpl.Lookup(shared.Name())
		if shared.Tree == nil || (existing != nil && existing.Tree != nil) {
			continue
		}
		if _, err := tmpl.New(shared.Name()).AddParseTree(shared.Name(), shared.Tree); err != nil {
			return nil, fmt.Errorf("apply shared template %s: %w", shared.Name(), err)
		}
	}

	assets := fstest.MapFS{}
	if err := copyFiles(assets, sharedFS, "shared/assets"); err != nil {
		return nil, fmt.Errorf("load shared assets: %w", err)
	}
	if err := copyFiles(assets, fsys, "assets"); err != nil {
		return nil, fmt.Errorf("load theme assets: %w", err)
	}

	return &Theme{Manifest: m, fsys: fsys, assets: assets, templates: tmpl}, nil
}

// Render executes the named template with data.
func (t *Theme) Render(w io.Writer, name string, data any) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

// Assets returns the theme's assets directory as a filesystem.
func (t *Theme) Assets() (fs.FS, error) {
	return t.assets, nil
}

// ContentVersion returns a deterministic version of the complete theme,
// including its manifest, templates, and assets.
func (t *Theme) ContentVersion() (string, error) {
	hash := sha256.New()
	err := hashFS(hash, "theme/", t.fsys)
	if err == nil {
		err = hashFS(hash, "shared/", sharedFS)
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func hashFS(hash io.Writer, prefix string, fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		if _, err := io.WriteString(hash, prefix+path+"\x00"); err != nil {
			return err
		}
		file, err := fsys.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func copyFiles(target fstest.MapFS, source fs.FS, root string) error {
	sub, err := fs.Sub(source, root)
	if err != nil {
		return err
	}
	return fs.WalkDir(sub, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		data, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target[strings.TrimPrefix(path, "./")] = &fstest.MapFile{
			Data: data, Mode: info.Mode(), ModTime: info.ModTime(),
		}
		return nil
	})
}

// AssetVersion returns a deterministic version for cache-busting asset URLs.
func (t *Theme) AssetVersion() (string, error) {
	assets, err := t.Assets()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	err = fs.WalkDir(assets, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		if _, err := io.WriteString(hash, path+"\x00"); err != nil {
			return err
		}
		file, err := assets.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil))[:12], nil
}

// Defaults returns the manifest's option defaults keyed by option key.
func (m Manifest) Defaults() map[string]any {
	out := make(map[string]any, len(m.Options))
	for _, o := range m.Options {
		out[o.Key] = o.Default
	}
	return out
}

// ResolveOptions overlays persisted theme settings on manifest defaults,
// preserving the declared option types.
func (m Manifest) ResolveOptions(settings map[string]string) map[string]any {
	out := m.Defaults()
	prefix := "theme." + m.Name + "."
	for _, option := range m.Options {
		raw, ok := settings[prefix+option.Key]
		if !ok {
			continue
		}
		switch option.Type {
		case "bool":
			if value, err := strconv.ParseBool(raw); err == nil {
				out[option.Key] = value
			}
		case "int":
			if value, err := strconv.Atoi(raw); err == nil {
				out[option.Key] = value
			}
		default:
			out[option.Key] = raw
		}
	}
	return out
}
