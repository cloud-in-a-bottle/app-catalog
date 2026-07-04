package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// colExists reports whether catalog_apps has the named column.
func colExists(t *testing.T, path, col string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM pragma_table_info('catalog_apps') WHERE name=?`, col,
	).Scan(&n); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	return n == 1
}

// seedSourceAndApp inserts a source and a single scored app via the store API.
func seedSourceAndApp(t *testing.T, st *Store, sourceID string, app CatalogApp) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateSource(ctx, Source{ID: sourceID, Name: sourceID, URL: "https://example.invalid/" + sourceID, Enabled: true}); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := st.MarkSourceSynced(ctx, sourceID, sourceID); err != nil {
		t.Fatalf("mark synced: %v", err)
	}
	app.SourceID = sourceID
	if err := st.ReplaceCatalogAppsForSource(ctx, sourceID, []CatalogApp{app}); err != nil {
		t.Fatalf("replace apps: %v", err)
	}
}

// TestFreshDBHasExplanationColumn confirms a brand-new database gets both score
// columns created by the migration (the base CREATE TABLE omits them).
func TestFreshDBHasExplanationColumn(t *testing.T) {
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

	if !colExists(t, path, "openhost_integration_score") {
		t.Error("fresh DB missing score column")
	}
	if !colExists(t, path, "openhost_integration_score_explanation") {
		t.Error("fresh DB missing explanation column")
	}
}

// TestMigrationRestorePreservesOtherData verifies that when the explanation
// column is dropped and re-added, the rest of an app's row (including its
// score) is preserved; only the explanation resets to empty.
func TestMigrationRestorePreservesOtherData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	seedSourceAndApp(t, st, "s", CatalogApp{
		AppID:                               "keep",
		Title:                               "Keep",
		Description:                         "desc",
		RepoURL:                             "https://example.invalid/keep",
		OpenhostIntegrationScore:            4,
		OpenhostIntegrationScoreExplanation: "will be dropped",
	})
	st.Close()

	// Drop the explanation column (simulating the prior schema).
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE catalog_apps DROP COLUMN openhost_integration_score_explanation`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	db.Close()

	// Re-open and migrate.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	defer st2.Close()
	if err := st2.Init(context.Background()); err != nil {
		t.Fatalf("init2: %v", err)
	}

	got, err := st2.GetCatalogApp(context.Background(), "s", "keep")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Keep" || got.Description != "desc" {
		t.Errorf("non-score data not preserved: %+v", got)
	}
	if got.OpenhostIntegrationScore != 4 {
		t.Errorf("score not preserved: got %d, want 4", got.OpenhostIntegrationScore)
	}
	if got.OpenhostIntegrationScoreExplanation != "" {
		t.Errorf("re-added explanation column should default empty, got %q", got.OpenhostIntegrationScoreExplanation)
	}

	// A fresh replace repopulates the explanation.
	if err := st2.ReplaceCatalogAppsForSource(context.Background(), "s", []CatalogApp{{
		SourceID:                            "s",
		AppID:                               "keep",
		Title:                               "Keep",
		RepoURL:                             "https://example.invalid/keep",
		OpenhostIntegrationScore:            4,
		OpenhostIntegrationScoreExplanation: "repopulated",
	}}); err != nil {
		t.Fatalf("replace2: %v", err)
	}
	got, err = st2.GetCatalogApp(context.Background(), "s", "keep")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if got.OpenhostIntegrationScoreExplanation != "repopulated" {
		t.Errorf("explanation not repopulated: %q", got.OpenhostIntegrationScoreExplanation)
	}
}

// TestMigrationManyInitCycles hammers Init repeatedly to prove the ADD/DROP
// migration statements stay idempotent across many upgrade cycles.
func TestMigrationManyInitCycles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.db")
	for i := 0; i < 25; i++ {
		st, err := Open(path)
		if err != nil {
			t.Fatalf("cycle %d open: %v", i, err)
		}
		if err := st.Init(context.Background()); err != nil {
			t.Fatalf("cycle %d init: %v", i, err)
		}
		st.Close()
	}
	if !colExists(t, path, "openhost_integration_score_explanation") {
		t.Error("explanation column missing after many init cycles")
	}
}

// TestReplaceManyAppsWithExplanations verifies a large batch of apps with mixed
// scores/explanations stores and reads back correctly (INSERT parameter
// alignment regression guard).
func TestReplaceManyAppsWithExplanations(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.CreateSource(ctx, Source{ID: "s", Name: "S", URL: "https://example.invalid/s", Enabled: true}); err != nil {
		t.Fatalf("source: %v", err)
	}
	if err := st.MarkSourceSynced(ctx, "s", "S"); err != nil {
		t.Fatalf("synced: %v", err)
	}

	const n = 200
	apps := make([]CatalogApp, n)
	for i := 0; i < n; i++ {
		score := i%6 - 1 // yields -1..4 across the range; store persists raw value
		if score < 0 {
			score = 0
		}
		expl := ""
		if score > 0 {
			expl = fmt.Sprintf("explanation for app %d", i)
		}
		apps[i] = CatalogApp{
			SourceID:                            "s",
			AppID:                               fmt.Sprintf("app-%03d", i),
			Title:                               fmt.Sprintf("App %d", i),
			RepoURL:                             fmt.Sprintf("https://example.invalid/app-%03d", i),
			OpenhostIntegrationScore:            score,
			OpenhostIntegrationScoreExplanation: expl,
		}
	}
	if err := st.ReplaceCatalogAppsForSource(ctx, "s", apps); err != nil {
		t.Fatalf("replace: %v", err)
	}

	got, err := st.ListCatalogApps(ctx, AppListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != n {
		t.Fatalf("got %d apps, want %d", len(got), n)
	}
	// Spot-check that each app's explanation matches its own row (no shifting).
	byID := map[string]CatalogApp{}
	for _, a := range got {
		byID[a.AppID] = a
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("app-%03d", i)
		a, ok := byID[id]
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if a.OpenhostIntegrationScore > 0 {
			want := fmt.Sprintf("explanation for app %d", i)
			if a.OpenhostIntegrationScoreExplanation != want {
				t.Errorf("%s: explanation = %q, want %q", id, a.OpenhostIntegrationScoreExplanation, want)
			}
		} else if a.OpenhostIntegrationScoreExplanation != "" {
			t.Errorf("%s: unrated app has explanation %q", id, a.OpenhostIntegrationScoreExplanation)
		}
	}
}
