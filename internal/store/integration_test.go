package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestIntegrationScoreRoundTrip verifies that the openhost_integration_score
// and openhost_integration_score_explanation columns store and read back
// correctly, and that apps without a score round-trip as zero / empty.
func TestIntegrationScoreRoundTrip(t *testing.T) {
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
			SourceID:                            "official",
			AppID:                               "searxng",
			Title:                               "SearXNG",
			Description:                         "Privacy-respecting metasearch",
			RepoURL:                             "https://example.invalid/searxng",
			OpenhostIntegrationScore:            5,
			OpenhostIntegrationScoreExplanation: "Stateless public search; nothing to leak.",
		},
		{
			SourceID:    "official",
			AppID:       "unrated",
			Title:       "Unrated",
			Description: "No integration score set",
			RepoURL:     "https://example.invalid/unrated",
		},
	}
	if err := store.ReplaceCatalogAppsForSource(ctx, "official", apps); err != nil {
		t.Fatalf("replace apps: %v", err)
	}

	got5, err := store.GetCatalogApp(ctx, "official", "searxng")
	if err != nil {
		t.Fatalf("get rated app: %v", err)
	}
	if got5.OpenhostIntegrationScore != 5 {
		t.Errorf("rated app: got score %d, want 5", got5.OpenhostIntegrationScore)
	}
	if got5.OpenhostIntegrationScoreExplanation != "Stateless public search; nothing to leak." {
		t.Errorf("rated app: got explanation %q, want it preserved", got5.OpenhostIntegrationScoreExplanation)
	}

	got0, err := store.GetCatalogApp(ctx, "official", "unrated")
	if err != nil {
		t.Fatalf("get unrated app: %v", err)
	}
	if got0.OpenhostIntegrationScore != 0 {
		t.Errorf("unrated app: got score %d, want 0", got0.OpenhostIntegrationScore)
	}
	if got0.OpenhostIntegrationScoreExplanation != "" {
		t.Errorf("unrated app: got explanation %q, want empty", got0.OpenhostIntegrationScoreExplanation)
	}
}

// TestListCatalogAppsOrderedByScore confirms that ListCatalogApps
// returns higher-rated apps first, with alphabetical title as the
// tiebreaker and unrated apps at the bottom.
func TestListCatalogAppsOrderedByScore(t *testing.T) {
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
		{SourceID: "s", AppID: "a1", Title: "A1", RepoURL: "https://example.invalid/a1", OpenhostIntegrationScore: 2},
		{SourceID: "s", AppID: "a2", Title: "A2", RepoURL: "https://example.invalid/a2", OpenhostIntegrationScore: 5},
		{SourceID: "s", AppID: "a3", Title: "A3", RepoURL: "https://example.invalid/a3", OpenhostIntegrationScore: 0},
		{SourceID: "s", AppID: "a4", Title: "A4", RepoURL: "https://example.invalid/a4", OpenhostIntegrationScore: 5},
	}
	if err := store.ReplaceCatalogAppsForSource(ctx, "s", apps); err != nil {
		t.Fatalf("replace apps: %v", err)
	}

	got, err := store.ListCatalogApps(ctx, AppListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	wantOrder := []string{"a2", "a4", "a1", "a3"}
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
// erroring on the duplicate column ALTERs.
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

// TestMigrationRestoresExplanationColumn verifies that a database whose
// openhost_integration_score_explanation column was dropped by the prior schema
// version has it re-added when Init runs the current migration. This is the
// upgrade path for instances that ran the (now reverted) drop migration.
func TestMigrationRestoresExplanationColumn(t *testing.T) {
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

	// Simulate the previously-merged schema in which the explanation column had
	// been dropped.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen raw: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE catalog_apps DROP COLUMN openhost_integration_score_explanation`); err != nil {
		t.Fatalf("drop legacy column: %v", err)
	}
	db.Close()

	// Re-running Init should re-add the column.
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
	var n int
	if err := db2.QueryRow(
		`SELECT count(*) FROM pragma_table_info('catalog_apps') WHERE name='openhost_integration_score_explanation'`,
	).Scan(&n); err != nil {
		t.Fatalf("query columns: %v", err)
	}
	if n != 1 {
		t.Fatalf("explanation column not present after migration (count=%d)", n)
	}

	// The migration must be idempotent: a third Init on the now-restored schema
	// must not fail on the duplicate ADD COLUMN.
	st3, err := Open(path)
	if err != nil {
		t.Fatalf("open 3: %v", err)
	}
	defer st3.Close()
	if err := st3.Init(context.Background()); err != nil {
		t.Fatalf("init 3 (migration not idempotent): %v", err)
	}
}
