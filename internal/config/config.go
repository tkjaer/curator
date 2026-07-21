// Package config resolves the on-disk locations Curator works with: the content
// root (the portable source of truth) and the output directory (disposable,
// rebuildable static site).
package config

import "path/filepath"

// Config holds the resolved paths for a single Curator site.
type Config struct {
	ContentRoot string
	OutputDir   string
}

// New returns a Config for the given content root and output directory,
// applying defaults when either is empty.
func New(contentRoot, outputDir string) Config {
	if contentRoot == "" {
		contentRoot = "."
	}
	if outputDir == "" {
		outputDir = filepath.Join(contentRoot, "output")
	}
	return Config{ContentRoot: contentRoot, OutputDir: outputDir}
}

// DBPath is the SQLite database file inside the content root.
func (c Config) DBPath() string {
	return filepath.Join(c.ContentRoot, "cms.db")
}

// OriginalsDir holds the uploaded full-resolution files.
func (c Config) OriginalsDir() string {
	return filepath.Join(c.ContentRoot, "originals")
}
