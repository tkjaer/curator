package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tkjaer/curator/internal/store"
)

func TestFormatVersion(t *testing.T) {
	tests := []struct {
		name     string
		revision string
		modified bool
		want     string
	}{
		{name: "revision", revision: "abcdef1234567890", want: "abcdef123456"},
		{name: "modified", revision: "abcdef1234567890", modified: true, want: "abcdef123456+modified"},
		{name: "no metadata", want: "development"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatVersion(test.revision, test.modified); got != test.want {
				t.Errorf("formatVersion() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestModuleBuildVersion(t *testing.T) {
	tests := []struct {
		moduleVersion string
		want          string
	}{
		{moduleVersion: "v0.0.0-20260811183600-c16f90d18e41+dirty", want: "c16f90d18e41+modified"},
		{moduleVersion: "v1.0.0", want: "v1.0.0"},
		{moduleVersion: "v1.0.0-beta.1", want: "v1.0.0-beta.1"},
		{moduleVersion: "(devel)", want: "development"},
	}
	for _, test := range tests {
		if got := moduleBuildVersion(test.moduleVersion); got != test.want {
			t.Errorf("moduleBuildVersion(%q) = %q, want %q", test.moduleVersion, got, test.want)
		}
	}
}

func TestAvailableThemesIncludesBundledThemes(t *testing.T) {
	found := map[string]bool{}
	for _, name := range availableThemes() {
		found[name] = true
	}
	for _, name := range []string{"default", "folio"} {
		if !found[name] {
			t.Errorf("availableThemes() = %v, want it to include %q", availableThemes(), name)
		}
	}
}

func TestLoadSiteThemeFallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cms.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Point the setting at a theme that does not exist; loading must fall back.
	if err := st.SetSetting(ctx, "site.theme", "does-not-exist"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSiteTheme(ctx, st); err != nil {
		t.Errorf("loadSiteTheme should fall back to default, got error: %v", err)
	}
}
