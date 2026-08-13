package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestSetSourceURLRepointsOnlyTheTarget covers the store side of moving a
// persisted feed URL off a renamed GitHub owner: the named source moves, other
// sources are untouched, and the source's identity and enabled state survive
// (a repoint must not silently disable or duplicate a feed).
func TestSetSourceURLRepointsOnlyTheTarget(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	const oldURL = "https://raw.githubusercontent.com/imbue-openhost/openhost-apps/main/catalog.json"
	const newURL = "https://raw.githubusercontent.com/cloud-in-a-bottle/openhost-apps/main/catalog.json"

	if err := store.CreateSource(ctx, Source{ID: "official", Name: "Official", URL: oldURL, Enabled: true}); err != nil {
		t.Fatalf("create official: %v", err)
	}
	if err := store.CreateSource(ctx, Source{
		ID:      "third-party",
		Name:    "Third Party",
		URL:     "https://example.invalid/other.json",
		Enabled: true,
	}); err != nil {
		t.Fatalf("create third-party: %v", err)
	}

	if err := store.SetSourceURL(ctx, "official", newURL); err != nil {
		t.Fatalf("set source url: %v", err)
	}

	official, err := store.GetSource(ctx, "official")
	if err != nil {
		t.Fatalf("get official: %v", err)
	}
	if official.URL != newURL {
		t.Fatalf("official URL = %q, want %q", official.URL, newURL)
	}
	if !official.Enabled {
		t.Fatal("repointing a source must not disable it")
	}
	if official.Name != "Official" {
		t.Fatalf("repointing changed the name to %q", official.Name)
	}

	other, err := store.GetSource(ctx, "third-party")
	if err != nil {
		t.Fatalf("get third-party: %v", err)
	}
	if other.URL != "https://example.invalid/other.json" {
		t.Fatalf("third-party URL changed to %q", other.URL)
	}

	sources, err := store.ListSources(ctx)
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources after a repoint, got %d", len(sources))
	}
}
