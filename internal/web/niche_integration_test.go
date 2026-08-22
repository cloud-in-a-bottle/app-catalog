package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cloud-in-a-bottle/app-catalog/internal/store"
)

// Reuses helpers from manual_integration_test.go in the same package:
// newTestServer, feedServer, addAndSync, get, feed, app, appWith.

func TestNicheIntegration(t *testing.T) {
	type tc struct {
		name string
		fn   func(t *testing.T)
	}
	cases := []tc{
		// --- Injection / escaping vectors via title ---
		{"title with template-injection braces escaped", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"ti","title":"{{.Secret}} ${jndi:ldap://x}","repo_url":"https://github.com/x/ti"}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			code, body := get(t, srv, "/apps/official/ti")
			if code != 200 {
				t.Fatalf("code %d", code)
			}
			if !strings.Contains(body, "{{.Secret}}") {
				t.Error("template-injection text should render as literal")
			}
		}},

		{"title with SQL-injection-y text stored verbatim, no corruption", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			payload := `'); DROP TABLE catalog_apps;-- and "); --`
			body := fmt.Sprintf(`{"name":"sqli","title":%q,"repo_url":"https://github.com/x/sqli"}`, payload)
			fs := feedServer(t, feed(body))
			defer fs.Close()
			if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
				t.Fatalf("sync: %v", err)
			}
			g, err := st.GetCatalogApp(context.Background(), "official", "sqli")
			if err != nil {
				t.Fatalf("get after sqli payload: %v", err)
			}
			if g.Title != payload {
				t.Errorf("payload mangled: %q", g.Title)
			}
			// Table intact: a second sync still works.
			if err := svc.SyncSource(context.Background(), "official"); err != nil {
				t.Fatalf("resync after sqli: %v", err)
			}
		}},

		// --- Malformed feeds ---
		{"feed wrong schema rejected", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			fs := feedServer(t, `{"schema":"openhost.catalog.v2","source_id":"s","apps":[]}`)
			defer fs.Close()
			if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err == nil {
				t.Error("wrong schema should be rejected")
			}
		}},

		{"feed truncated JSON rejected", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			fs := feedServer(t, `{"schema":"openhost.catalog.v1","apps":[{"name":"a"`)
			defer fs.Close()
			if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err == nil {
				t.Error("truncated JSON should be rejected")
			}
		}},

		{"feed empty apps list yields no rows but no error", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(""))
			defer fs.Close()
			if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
				t.Fatalf("empty apps should sync cleanly: %v", err)
			}
			code, _ := get(t, srv, "/apps/official/whatever")
			if code != 404 {
				t.Errorf("expected 404, got %d", code)
			}
		}},

		// --- Duplicate handling ---
		{"duplicate app ids within one feed rejected", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			body := feed(app("dup", "Dup1") + "," + app("dup", "Dup2"))
			fs := feedServer(t, body)
			defer fs.Close()
			if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err == nil {
				t.Error("duplicate app id within a source should be rejected")
			}
		}},

		// --- Sort stability ---
		{"apps sort alphabetically by title case-insensitively", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				app("c", "charlie"),
				app("a", "Alpha"),
				app("b", "bravo"),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced&filter=1")
			ia := strings.Index(page, "Alpha")
			ib := strings.Index(page, "bravo")
			ic := strings.Index(page, "charlie")
			if !(ia < ib && ib < ic) {
				t.Errorf("case-insensitive title sort failed: A=%d b=%d c=%d", ia, ib, ic)
			}
		}},

		// --- Multi-source serialized sync ---
		{"many sources synced in sequence stay consistent", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			servers := make([]*httptest.Server, 5)
			for i := 0; i < 5; i++ {
				name := fmt.Sprintf("appc%d", i)
				fs := feedServer(t, feed(app(name, "App"+name)))
				servers[i] = fs
				if err := st.CreateSource(context.Background(), store.Source{ID: fmt.Sprintf("src%d", i), Name: fmt.Sprintf("S%d", i), URL: fs.URL + "/catalog.json", Enabled: true}); err != nil {
					t.Fatalf("create src%d: %v", i, err)
				}
			}
			defer func() {
				for _, s := range servers {
					s.Close()
				}
			}()
			for i := 0; i < 5; i++ {
				if err := svc.SyncSource(context.Background(), fmt.Sprintf("src%d", i)); err != nil {
					t.Errorf("sync src%d failed: %v", i, err)
				}
			}
			for i := 0; i < 5; i++ {
				name := fmt.Sprintf("appc%d", i)
				if _, err := st.GetCatalogApp(context.Background(), fmt.Sprintf("src%d", i), name); err != nil {
					t.Errorf("missing app %s after sync: %v", name, err)
				}
			}
			_, page := get(t, srv, "/?advanced&filter=1")
			if !strings.Contains(page, "Appappc0") {
				t.Error("synced app missing from listing")
			}
		}},

		// --- Concurrent READS during repeated syncs stay safe ---
		{"concurrent reads during repeated syncs never error", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(app("rc", "ReadConcurrent"))
			fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")

			stop := make(chan struct{})
			readErr := make(chan string, 256)

			var writer sync.WaitGroup
			writer.Add(1)
			go func() {
				defer writer.Done()
				for {
					select {
					case <-stop:
						return
					default:
						_ = svc.SyncSource(context.Background(), "official")
					}
				}
			}()

			var readers sync.WaitGroup
			for r := 0; r < 8; r++ {
				readers.Add(1)
				go func() {
					defer readers.Done()
					for k := 0; k < 15; k++ {
						if code, b := get(t, srv, "/apps/official/rc"); code != 200 {
							readErr <- fmt.Sprintf("detail code %d", code)
						} else if !strings.Contains(b, "ReadConcurrent") {
							readErr <- "detail missing app title during sync"
						}
						if code, _ := get(t, srv, "/?advanced&filter=1"); code != 200 {
							readErr <- fmt.Sprintf("listing code %d", code)
						}
					}
				}()
			}

			readers.Wait()
			close(stop)
			writer.Wait()
			close(readErr)
			for msg := range readErr {
				t.Errorf("reader saw error during sync: %s", msg)
			}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, c.fn)
	}
}

// TestFilterHelpers covers the pure URL-building and expression-parsing
// helpers that power the filter UI.
func TestFilterHelpers(t *testing.T) {
	// --- isActiveTag ---
	t.Run("isActiveTag/exact match", func(t *testing.T) {
		if !isActiveTag("rss", "rss") {
			t.Error("exact match should be active")
		}
	})

	t.Run("isActiveTag/no match", func(t *testing.T) {
		if isActiveTag("rss", "email") {
			t.Error("non-matching tag should not be active")
		}
	})

	t.Run("isActiveTag/term in OR expression", func(t *testing.T) {
		if !isActiveTag("rss || email", "rss") {
			t.Error("rss should be active in OR expression")
		}
		if !isActiveTag("rss || email", "email") {
			t.Error("email should be active in OR expression")
		}
		if isActiveTag("rss || email", "privacy") {
			t.Error("privacy not in expression should not be active")
		}
	})

	t.Run("isActiveTag/nested term is not top-level active", func(t *testing.T) {
		if isActiveTag("(rss && email) || privacy", "rss") {
			t.Error("term nested in sub-expression should not be top-level active")
		}
		if !isActiveTag("(rss && email) || privacy", "privacy") {
			t.Error("top-level term should be active")
		}
	})

	t.Run("isActiveTag/empty expression", func(t *testing.T) {
		if isActiveTag("", "rss") {
			t.Error("empty expr should have nothing active")
		}
	})

	// --- isActiveCat ---
	t.Run("isActiveCat/simple category active", func(t *testing.T) {
		if !isActiveCat("privacy", "", "privacy") {
			t.Error("simple category should be active")
		}
		if isActiveCat("privacy", "", "ai") {
			t.Error("different category should not be active")
		}
	})

	t.Run("isActiveCat/all sentinel never active", func(t *testing.T) {
		if isActiveCat("all", "", "privacy") {
			t.Error("all sentinel should not activate any specific category")
		}
	})

	t.Run("isActiveCat/empty never active", func(t *testing.T) {
		if isActiveCat("", "", "privacy") {
			t.Error("empty filter should not activate anything")
		}
	})

	t.Run("isActiveCat/custom expression", func(t *testing.T) {
		if !isActiveCat("custom", "privacy || ai", "privacy") {
			t.Error("privacy should be active in custom OR expression")
		}
		if !isActiveCat("custom", "privacy || ai", "ai") {
			t.Error("ai should be active in custom OR expression")
		}
		if isActiveCat("custom", "privacy || ai", "search") {
			t.Error("search not in expression should not be active")
		}
	})

	t.Run("isActiveCat/custom with empty expression", func(t *testing.T) {
		if isActiveCat("custom", "", "privacy") {
			t.Error("custom mode with empty expression should not activate anything")
		}
	})

	// --- splitTopLevelOr ---
	t.Run("splitTopLevelOr/single term", func(t *testing.T) {
		parts := splitTopLevelOr("privacy")
		if len(parts) != 1 || strings.TrimSpace(parts[0]) != "privacy" {
			t.Errorf("got %v", parts)
		}
	})

	t.Run("splitTopLevelOr/two terms", func(t *testing.T) {
		parts := splitTopLevelOr("privacy || ai")
		if len(parts) != 2 {
			t.Fatalf("want 2 parts, got %d: %v", len(parts), parts)
		}
		if strings.TrimSpace(parts[0]) != "privacy" || strings.TrimSpace(parts[1]) != "ai" {
			t.Errorf("unexpected parts: %v", parts)
		}
	})

	t.Run("splitTopLevelOr/three terms", func(t *testing.T) {
		parts := splitTopLevelOr("ai || privacy || search")
		if len(parts) != 3 {
			t.Fatalf("want 3 parts, got %d: %v", len(parts), parts)
		}
	})

	t.Run("splitTopLevelOr/nested parens not split", func(t *testing.T) {
		parts := splitTopLevelOr("(ai || privacy) || search")
		if len(parts) != 2 {
			t.Fatalf("want 2 top-level parts, got %d: %v", len(parts), parts)
		}
	})

	t.Run("splitTopLevelOr/single pipe normalized", func(t *testing.T) {
		parts := splitTopLevelOr("ai | privacy")
		if len(parts) != 2 {
			t.Fatalf("single | should split same as ||, got %d parts: %v", len(parts), parts)
		}
	})

	// --- removeTopLevelOrTerm ---
	t.Run("removeTopLevelOrTerm/removes only term", func(t *testing.T) {
		expr, removed := removeTopLevelOrTerm("privacy", "privacy")
		if !removed {
			t.Error("should report removed=true")
		}
		if expr != "" {
			t.Errorf("got %q, want empty string", expr)
		}
	})

	t.Run("removeTopLevelOrTerm/removes from middle", func(t *testing.T) {
		expr, removed := removeTopLevelOrTerm("ai || privacy || search", "privacy")
		if !removed {
			t.Error("should report removed=true")
		}
		if expr != "ai || search" {
			t.Errorf("got %q, want %q", expr, "ai || search")
		}
	})

	t.Run("removeTopLevelOrTerm/not found leaves expr unchanged", func(t *testing.T) {
		expr, removed := removeTopLevelOrTerm("ai || privacy", "search")
		if removed {
			t.Error("should report removed=false")
		}
		if expr != "ai || privacy" {
			t.Errorf("expr should be unchanged, got %q", expr)
		}
	})

	t.Run("removeTopLevelOrTerm/does not match nested term", func(t *testing.T) {
		_, removed := removeTopLevelOrTerm("(ai && privacy) || search", "ai")
		if removed {
			t.Error("term nested inside sub-expression should not be removed as top-level")
		}
	})

	// --- catChipURL ---
	t.Run("catChipURL/first click adds category", func(t *testing.T) {
		got := catChipURL("", "", "", "privacy", "")
		if !strings.Contains(got, "category=privacy") {
			t.Errorf("first click should set category=privacy, got %q", got)
		}
	})

	t.Run("catChipURL/click active category clears filter", func(t *testing.T) {
		got := catChipURL("", "privacy", "", "privacy", "")
		if strings.Contains(got, "category=") {
			t.Errorf("clicking active category should remove the category param, got %q", got)
		}
	})

	t.Run("catChipURL/second category switches to custom OR expression", func(t *testing.T) {
		got := catChipURL("", "ai", "", "privacy", "")
		if !strings.Contains(got, "category=custom") {
			t.Errorf("second category click should switch to custom mode, got %q", got)
		}
		if !strings.Contains(got, "ai") || !strings.Contains(got, "privacy") {
			t.Errorf("OR expression should contain both categories, got %q", got)
		}
	})

	t.Run("catChipURL/removing term from OR expression", func(t *testing.T) {
		// currentExpr = "ai || privacy" (custom mode); clicking privacy removes it.
		got := catChipURL("", "custom", "ai || privacy", "privacy", "")
		if strings.Contains(got, "privacy") {
			t.Errorf("removed term should not appear in URL, got %q", got)
		}
		if !strings.Contains(got, "ai") {
			t.Errorf("remaining term should still be present, got %q", got)
		}
	})

	t.Run("catChipURL/preserves tag expression", func(t *testing.T) {
		got := catChipURL("", "", "", "privacy", "rss")
		if !strings.Contains(got, "tag_expr=rss") {
			t.Errorf("tag_expr should be preserved in URL, got %q", got)
		}
	})

	t.Run("catChipURL/with base path prefix", func(t *testing.T) {
		got := catChipURL("/catalog", "", "", "privacy", "")
		if !strings.HasPrefix(got, "/catalog/") {
			t.Errorf("URL should start with base path, got %q", got)
		}
	})

	// --- tagClickURL ---
	t.Run("tagClickURL/first click adds tag", func(t *testing.T) {
		got := tagClickURL("", "", "rss", "", "")
		if !strings.Contains(got, "tag_expr=rss") {
			t.Errorf("first click should set tag_expr=rss, got %q", got)
		}
	})

	t.Run("tagClickURL/click active tag clears filter", func(t *testing.T) {
		got := tagClickURL("", "rss", "rss", "", "")
		if strings.Contains(got, "tag_expr=") {
			t.Errorf("clicking active tag should remove tag_expr, got %q", got)
		}
	})

	t.Run("tagClickURL/second click builds OR expression", func(t *testing.T) {
		got := tagClickURL("", "rss", "email", "", "")
		if !strings.Contains(got, "rss") || !strings.Contains(got, "email") {
			t.Errorf("second tag click should include both tags, got %q", got)
		}
	})

	t.Run("tagClickURL/removing tag from OR expression", func(t *testing.T) {
		got := tagClickURL("", "rss || email", "rss", "", "")
		if strings.Contains(got, "rss") {
			t.Errorf("removed tag should not appear, got %q", got)
		}
		if !strings.Contains(got, "email") {
			t.Errorf("remaining tag should still be present, got %q", got)
		}
	})

	t.Run("tagClickURL/preserves category filter", func(t *testing.T) {
		got := tagClickURL("", "", "rss", "privacy", "")
		if !strings.Contains(got, "category=privacy") {
			t.Errorf("category filter should be preserved in URL, got %q", got)
		}
	})

	t.Run("tagClickURL/preserves custom category expression", func(t *testing.T) {
		got := tagClickURL("", "", "rss", "custom", "ai || privacy")
		if !strings.Contains(got, "category=custom") {
			t.Errorf("custom category mode should be preserved, got %q", got)
		}
		if !strings.Contains(got, "category_expr=") {
			t.Errorf("category_expr should be preserved, got %q", got)
		}
	})
}
