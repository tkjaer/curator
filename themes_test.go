package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tkjaer/curator/internal/store"
)

func TestAvailableThemesIncludesDefault(t *testing.T) {
	found := false
	for _, name := range availableThemes() {
		if name == "default" {
			found = true
		}
	}
	if !found {
		t.Errorf("availableThemes() = %v, want it to include \"default\"", availableThemes())
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
