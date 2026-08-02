package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tkjaer/curator/internal/config"
)

func TestEnsureContentRoot(t *testing.T) {
	cfg := config.New(filepath.Join(t.TempDir(), "site"), "")
	if err := ensureContentRoot(cfg); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{cfg.ContentRoot, cfg.OriginalsDir()} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Errorf("content directory %q not created: info=%v err=%v", dir, info, err)
		}
	}
}
