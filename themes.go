package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"sort"

	"github.com/tkjaer/curator/internal/store"
	"github.com/tkjaer/curator/internal/theme"
)

//go:embed themes
var themesFS embed.FS

// loadTheme loads a theme by name from the embedded themes directory.
func loadTheme(name string) (*theme.Theme, error) {
	sub, err := fs.Sub(themesFS, "themes/"+name)
	if err != nil {
		return nil, err
	}
	return theme.Load(sub)
}

// availableThemes lists the embedded themes (directories with a manifest.json),
// sorted by name.
func availableThemes() []string {
	entries, err := fs.ReadDir(themesFS, "themes")
	if err != nil {
		return []string{"default"}
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := fs.Stat(themesFS, "themes/"+e.Name()+"/manifest.json"); err == nil {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return []string{"default"}
	}
	sort.Strings(names)
	return names
}

// loadSiteTheme loads the theme named by the site.theme setting, falling back to
// the default theme when the setting is unset or the theme cannot be loaded.
func loadSiteTheme(ctx context.Context, st *store.Store) (*theme.Theme, error) {
	name := "default"
	if settings, err := st.Settings(ctx); err == nil {
		if t := settings["site.theme"]; t != "" {
			name = t
		}
	}
	th, err := loadTheme(name)
	if err != nil {
		if name == "default" {
			return nil, err
		}
		log.Printf("theme %q could not be loaded (%v); falling back to default", name, err)
		return loadTheme("default")
	}
	return th, nil
}
