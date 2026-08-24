package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestCatalogAppRoundTrip verifies that a catalog app stores and reads
// back correctly through the store.
func TestCatalogAppRoundTrip(t *testing.T) {
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

	if err := store.CreateSource(ctx, Source{
		ID:      "official",
		Name:    "OpenHost Official",
		URL:     "https://example.invalid/catalog.json",
		Enabled: true,
	}); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := store.MarkSourceSynced(ctx, "official", "OpenHost Official"); err != nil {
		t.Fatalf("mark synced: %v", err)
	}

	apps := []CatalogApp{
		{
			SourceID:    "official",
			AppID:       "searxng",
			Title:       "SearXNG",
			Description: "Privacy-respecting metasearch",
			RepoURL:     "https://example.invalid/searxng",
		},
	}
	if err := store.ReplaceCatalogAppsForSource(ctx, "official", apps); err != nil {
		t.Fatalf("replace apps: %v", err)
	}

	got, err := store.GetCatalogApp(ctx, "official", "searxng")
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if got.Title != "SearXNG" {
		t.Errorf("title: got %q, want %q", got.Title, "SearXNG")
	}
	if got.RepoURL != "https://example.invalid/searxng" {
		t.Errorf("repo_url: got %q", got.RepoURL)
	}
}

// TestListCatalogAppsOrderedByTitle confirms that ListCatalogApps
// returns apps alphabetically by title, with app ID as the tiebreaker.
func TestListCatalogAppsOrderedByTitle(t *testing.T) {
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
	if err := store.CreateSource(ctx, Source{
		ID:      "s",
		Name:    "S",
		URL:     "https://example.invalid/s.json",
		Enabled: true,
	}); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := store.MarkSourceSynced(ctx, "s", "S"); err != nil {
		t.Fatalf("mark synced: %v", err)
	}

	apps := []CatalogApp{
		{SourceID: "s", AppID: "z-app", Title: "Zulu", RepoURL: "https://example.invalid/z"},
		{SourceID: "s", AppID: "a-app", Title: "Alpha", RepoURL: "https://example.invalid/a"},
		{SourceID: "s", AppID: "m2", Title: "Mike", RepoURL: "https://example.invalid/m2"},
		{SourceID: "s", AppID: "m1", Title: "Mike", RepoURL: "https://example.invalid/m1"},
	}
	if err := store.ReplaceCatalogAppsForSource(ctx, "s", apps); err != nil {
		t.Fatalf("replace apps: %v", err)
	}

	got, err := store.ListCatalogApps(ctx, AppListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Alphabetical by title (Alpha, Mike, Zulu); the two "Mike" rows
	// break the tie by app ID (m1 before m2).
	wantOrder := []string{"a-app", "m1", "m2", "z-app"}
	if len(got) != len(wantOrder) {
		t.Fatalf("want %d apps, got %d", len(wantOrder), len(got))
	}
	for i, id := range wantOrder {
		if got[i].AppID != id {
			ids := make([]string, len(got))
			for j, a := range got {
				ids[j] = a.AppID
			}
			t.Fatalf("order mismatch at index %d: got %v, want %v", i, ids, wantOrder)
		}
	}
}

// TestIntegrationMigrationIdempotent checks that re-running Init
// against an already-migrated database is a no-op rather than
// erroring on the duplicate/no-such column ALTERs.
func TestIntegrationMigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.db")
	store1, err := Open(path)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	if err := store1.Init(context.Background()); err != nil {
		t.Fatalf("init 1: %v", err)
	}
	store1.Close()

	store2, err := Open(path)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	defer store2.Close()
	if err := store2.Init(context.Background()); err != nil {
		t.Fatalf("init 2 (migration not idempotent): %v", err)
	}
}

// TestMigrationDropsLegacyScoreColumns verifies that a database carrying the
// removed openhost_integration_score and openhost_integration_score_explanation
// columns (from prior schema versions) has them dropped when Init runs the
// current migration. Curation is now include/exclude at the feed level, so the
// score columns no longer exist.
func TestMigrationDropsLegacyScoreColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	st.Close()

	// Simulate previously-merged schema versions by re-adding the columns.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen raw: %v", err)
	}
	legacy := []string{
		`ALTER TABLE catalog_apps ADD COLUMN openhost_integration_score INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE catalog_apps ADD COLUMN openhost_integration_score_explanation TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range legacy {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed legacy column: %v", err)
		}
	}
	db.Close()

	// Re-running Init should drop both columns.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	defer st2.Close()
	if err := st2.Init(context.Background()); err != nil {
		t.Fatalf("init 2: %v", err)
	}

	db2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("verify open: %v", err)
	}
	defer db2.Close()
	for _, col := range []string{"openhost_integration_score", "openhost_integration_score_explanation"} {
		var n int
		if err := db2.QueryRow(
			`SELECT count(*) FROM pragma_table_info('catalog_apps') WHERE name=?`, col,
		).Scan(&n); err != nil {
			t.Fatalf("query columns: %v", err)
		}
		if n != 0 {
			t.Fatalf("column %q still present after migration (count=%d)", col, n)
		}
	}
}
