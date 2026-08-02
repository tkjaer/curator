package deploy

import (
	"context"
	"strings"
	"testing"
)

func TestRsyncRequiresBuiltSite(t *testing.T) {
	err := Rsync(context.Background(), t.TempDir(), Options{Target: "example.com:/srv/site", Delete: true})
	if err == nil || !strings.Contains(err.Error(), "index.html not found") {
		t.Fatalf("Rsync error = %v, want missing index.html", err)
	}
}

func TestRsyncRequiresDestination(t *testing.T) {
	err := Rsync(context.Background(), t.TempDir(), Options{})
	if err == nil || !strings.Contains(err.Error(), "destination is required") {
		t.Fatalf("Rsync error = %v, want missing destination", err)
	}
}
