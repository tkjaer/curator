package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tkjaer/curator/internal/store"
)

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
