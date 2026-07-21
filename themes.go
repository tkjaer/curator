package main

import (
	"embed"
	"io/fs"

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
