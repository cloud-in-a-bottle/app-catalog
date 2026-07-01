package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imbue-openhost/openhost-catalog/internal/catalog"
	"github.com/imbue-openhost/openhost-catalog/internal/config"
	"github.com/imbue-openhost/openhost-catalog/internal/store"
)

// feedServer serves a fixed JSON body at /catalog.json.
func feedServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

// newTestServer builds a Server backed by a temp DB, plus the catalog service
// used to sync.
func newTestServer(t *testing.T) (*Server, *catalog.Service, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg := config.Config{
		AppName:          "openhost-catalog",
		RouterURL:        "http://router.invalid",
		DefaultSourceURL: "",
	}
	srv, err := NewServer(cfg, st)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	svc := catalog.NewService(st, &http.Client{})
	return srv, svc, st
}

func addAndSync(t *testing.T, svc *catalog.Service, st *store.Store, id, name, url string) error {
	t.Helper()
	if err := st.CreateSource(context.Background(), store.Source{ID: id, Name: name, URL: url, Enabled: true}); err != nil {
		t.Fatalf("create source %s: %v", id, err)
	}
	return svc.SyncSource(context.Background(), id)
}

func get(t *testing.T, srv *Server, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func feed(apps string) string {
	return `{"schema":"openhost.catalog.v1","source_id":"official","source_name":"OpenHost Official","generated_at":"2026-01-01T00:00:00Z","apps":[` + apps + `]}`
}

// app builds a minimal feed entry with name, title, and repo_url.
func app(name, title string) string {
	return fmt.Sprintf(`{"name":%q,"title":%q,"repo_url":"https://github.com/x/%s"}`, name, title, name)
}

// appWith builds a feed entry with tags and categories.
func appWith(name, title string, tags, cats []string) string {
	tagsJSON, _ := json.Marshal(tags)
	catsJSON, _ := json.Marshal(cats)
	return fmt.Sprintf(`{"name":%q,"title":%q,"repo_url":"https://github.com/x/%s","tags":%s,"categories":%s}`,
		name, title, name, tagsJSON, catsJSON)
}

// appWithDesc builds a feed entry with a description field.
func appWithDesc(name, title, desc string) string {
	return fmt.Sprintf(`{"name":%q,"title":%q,"repo_url":"https://github.com/x/%s","description":%q}`,
		name, title, name, desc)
}

func TestManualIntegration(t *testing.T) {
	type tc struct {
		name string
		fn   func(t *testing.T)
	}
	cases := []tc{
		// --- Detail page ---
		{"detail title XSS escaped", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"t","title":"<img src=x onerror=alert(1)>","repo_url":"https://github.com/x/t"}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/t")
			if strings.Contains(body, "<img src=x onerror=alert(1)>") {
				t.Error("title not escaped")
			}
		}},

		{"detail 404 for missing app", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("real", "Real")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			code, _ := get(t, srv, "/apps/official/doesnotexist")
			if code != 404 {
				t.Errorf("code %d want 404", code)
			}
		}},

		{"multi-source same app id isolated", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fsA := feedServer(t, feed(app("dup", "DupA")))
			defer fsA.Close()
			fsB := feedServer(t, feed(app("dup", "DupB")))
			defer fsB.Close()
			addAndSync(t, svc, st, "srcA", "A", fsA.URL+"/catalog.json")
			addAndSync(t, svc, st, "srcB", "B", fsB.URL+"/catalog.json")
			_, bA := get(t, srv, "/apps/srcA/dup")
			_, bB := get(t, srv, "/apps/srcB/dup")
			if !strings.Contains(bA, "DupA") {
				t.Error("srcA shows wrong title")
			}
			if !strings.Contains(bB, "DupB") {
				t.Error("srcB shows wrong title")
			}
		}},

		// --- Text search ---
		{"text search finds app by title", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("miniflux", "Miniflux")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?q=miniflux&filter=1")
			if !strings.Contains(page, "Miniflux") {
				t.Error("title search did not return matching app")
			}
		}},

		{"text search finds app by description", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(appWithDesc("rss", "RSS Reader", "self-hosted feed aggregator")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?q=aggregator&filter=1")
			if !strings.Contains(page, "RSS Reader") {
				t.Error("description search did not return matching app")
			}
		}},

		{"text search is case-insensitive", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("ghost", "Ghost")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?q=GHOST&filter=1")
			if !strings.Contains(page, "Ghost") {
				t.Error("case-insensitive search failed")
			}
		}},

		{"text search no match yields empty listing", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("ghost", "Ghost")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?q=zzznomatch&filter=1")
			if strings.Contains(page, "Ghost") {
				t.Error("non-matching search returned unexpected app")
			}
		}},

		{"main page search also matches tags and categories", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(appWith("searxng", "SearXNG", []string{"metasearch"}, []string{"search"})))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			// Main search (no ?advanced) uses SearchAll=true: matches tags and categories too.
			_, page := get(t, srv, "/?q=metasearch")
			if !strings.Contains(page, "SearXNG") {
				t.Error("main search should match on tag")
			}
			_, page2 := get(t, srv, "/?q=search")
			if !strings.Contains(page2, "SearXNG") {
				t.Error("main search should match on category")
			}
		}},

		{"advanced search does not match tags or categories", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(appWith("searxng", "SearXNG", []string{"metasearch"}, []string{"search"})))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			// Advanced search (SearchAll=false) only matches title, description, app_id.
			_, page := get(t, srv, "/?advanced&q=metasearch&filter=1")
			if strings.Contains(page, "SearXNG") {
				t.Error("advanced search should not match on tags")
			}
		}},

		// --- Category filter ---
		{"category filter returns only matching apps", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				appWith("privapp", "PrivApp", nil, []string{"privacy"}),
				appWith("aiapp", "AIApp", nil, []string{"ai"}),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			// Simple category click (no ?advanced, no ?filter) shows matching apps.
			_, page := get(t, srv, "/?category=privacy")
			if !strings.Contains(page, "PrivApp") {
				t.Error("expected privacy app in category results")
			}
			if strings.Contains(page, "AIApp") {
				t.Error("unexpected ai app in privacy results")
			}
		}},

		{"category=all sentinel shows all apps", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				appWith("a", "AppA", nil, []string{"privacy"}),
				appWith("b", "AppB", nil, []string{"ai"}),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?category=all")
			if !strings.Contains(page, "AppA") || !strings.Contains(page, "AppB") {
				t.Error("category=all should show all apps regardless of category")
			}
		}},

		{"category custom OR expression matches either category", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				appWith("priv", "PrivApp", nil, []string{"privacy"}),
				appWith("ai", "AIApp", nil, []string{"ai"}),
				appWith("prod", "ProdApp", nil, []string{"productivity"}),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			// privacy || ai
			_, page := get(t, srv, "/?advanced&filter=1&category=custom&category_expr=privacy+%7C%7C+ai")
			if !strings.Contains(page, "PrivApp") || !strings.Contains(page, "AIApp") {
				t.Error("OR expression should match both privacy and ai apps")
			}
			if strings.Contains(page, "ProdApp") {
				t.Error("OR expression should not match productivity app")
			}
		}},

		{"category custom AND expression requires both categories", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				appWith("both", "BothApp", nil, []string{"privacy", "ai"}),
				appWith("onlyprivacy", "OnlyPrivacy", nil, []string{"privacy"}),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			// privacy && ai
			_, page := get(t, srv, "/?advanced&filter=1&category=custom&category_expr=privacy+%26%26+ai")
			if !strings.Contains(page, "BothApp") {
				t.Error("AND expression should match app with both categories")
			}
			if strings.Contains(page, "OnlyPrivacy") {
				t.Error("AND expression should not match app with only one category")
			}
		}},

		{"unknown category in feed silently dropped at ingest", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			// "monitoring" is not in AllowedCategories; "utility" is.
			fs := feedServer(t, feed(appWith("mon", "Monitor", nil, []string{"monitoring", "utility"})))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			code, _ := get(t, srv, "/apps/official/mon")
			if code != 200 {
				t.Errorf("app with unknown category should still be accessible, got %d", code)
			}
			_, page := get(t, srv, "/?category=utility")
			if !strings.Contains(page, "Monitor") {
				t.Error("app should appear under its valid category")
			}
			_, pageM := get(t, srv, "/?category=monitoring")
			if strings.Contains(pageM, "Monitor") {
				t.Error("unknown category should have been stripped; app should not appear under it")
			}
		}},

		// --- Tag filter ---
		{"tag filter single tag returns only matching apps", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				appWith("a", "AppA", []string{"rss"}, nil),
				appWith("b", "AppB", []string{"email"}, nil),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced&filter=1&tag_expr=rss")
			if !strings.Contains(page, "AppA") {
				t.Error("rss-tagged app missing from tag filter results")
			}
			if strings.Contains(page, "AppB") {
				t.Error("email-tagged app should not appear in rss filter results")
			}
		}},

		{"tag OR expression returns apps with either tag", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				appWith("a", "AppA", []string{"rss"}, nil),
				appWith("b", "AppB", []string{"email"}, nil),
				appWith("c", "AppC", []string{"other"}, nil),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			// rss || email
			_, page := get(t, srv, "/?advanced&filter=1&tag_expr=rss+%7C%7C+email")
			if !strings.Contains(page, "AppA") || !strings.Contains(page, "AppB") {
				t.Error("OR tag filter should return apps with either tag")
			}
			if strings.Contains(page, "AppC") {
				t.Error("app with neither tag should not appear")
			}
		}},

		{"tag AND expression requires both tags", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				appWith("both", "BothTags", []string{"rss", "privacy"}, nil),
				appWith("one", "OneTag", []string{"rss"}, nil),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			// rss && privacy
			_, page := get(t, srv, "/?advanced&filter=1&tag_expr=rss+%26%26+privacy")
			if !strings.Contains(page, "BothTags") {
				t.Error("AND tag filter should match app with both tags")
			}
			if strings.Contains(page, "OneTag") {
				t.Error("AND tag filter should not match app with only one tag")
			}
		}},

		{"single pipe and ampersand normalized to double operators", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				appWith("a", "AppA", []string{"rss"}, nil),
				appWith("b", "AppB", []string{"email"}, nil),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			// Single | should work the same as ||
			_, page := get(t, srv, "/?advanced&filter=1&tag_expr=rss+%7C+email")
			if !strings.Contains(page, "AppA") || !strings.Contains(page, "AppB") {
				t.Error("single | should be treated as OR")
			}
		}},

		// --- Source filter ---
		{"source filter narrows results to one source", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fsA := feedServer(t, feed(app("app", "AppFromA")))
			fsB := feedServer(t, feed(app("app", "AppFromB")))
			defer fsA.Close()
			defer fsB.Close()
			addAndSync(t, svc, st, "srcA", "A", fsA.URL+"/catalog.json")
			addAndSync(t, svc, st, "srcB", "B", fsB.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced&filter=1&source=srcA")
			if !strings.Contains(page, "AppFromA") {
				t.Error("source filter should show srcA app")
			}
			if strings.Contains(page, "AppFromB") {
				t.Error("source filter should hide srcB app")
			}
		}},

		// --- Combined filters ---
		{"category and tag filter applied together", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				appWith("match", "Match", []string{"rss"}, []string{"privacy"}),
				appWith("wrongtag", "WrongTag", []string{"email"}, []string{"privacy"}),
				appWith("wrongcat", "WrongCat", []string{"rss"}, []string{"ai"}),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced&filter=1&category=privacy&tag_expr=rss")
			if !strings.Contains(page, "Match") {
				t.Error("app matching both category and tag should appear")
			}
			if strings.Contains(page, "WrongTag") || strings.Contains(page, "WrongCat") {
				t.Error("apps not satisfying both filters should be excluded")
			}
		}},

		// --- Visibility logic ---
		{"advanced mode hides apps until filter submitted", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("ghost", "Ghost")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced")
			if strings.Contains(page, "Ghost") {
				t.Error("advanced mode should hide apps before filter is submitted")
			}
			_, page2 := get(t, srv, "/?advanced&filter=1")
			if !strings.Contains(page2, "Ghost") {
				t.Error("advanced mode should show apps after filter is submitted")
			}
		}},

		{"category chip shows apps without explicit filter param", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(appWith("ghost", "Ghost", nil, []string{"publishing"})))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			// Clicking a category chip produces /?category=publishing (no ?filter).
			_, page := get(t, srv, "/?category=publishing")
			if !strings.Contains(page, "Ghost") {
				t.Error("category chip navigation should show matching apps without ?filter param")
			}
		}},

		// --- Listing order ---
		{"listing sorts apps alphabetically by title", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				app("c", "Charlie"),
				app("a", "Alpha"),
				app("b", "Bravo"),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced&filter=1")
			iA := strings.Index(page, "Alpha")
			iB := strings.Index(page, "Bravo")
			iC := strings.Index(page, "Charlie")
			if !(iA >= 0 && iB >= 0 && iC >= 0 && iA < iB && iB < iC) {
				t.Errorf("expected Alpha < Bravo < Charlie in page, got positions %d %d %d", iA, iB, iC)
			}
		}},

		// --- Resync ---
		{"re-sync updates app title", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(app("up", "OldTitle"))
			fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, b1 := get(t, srv, "/apps/official/up")
			if !strings.Contains(b1, "OldTitle") {
				t.Fatal("setup failed: initial title not found")
			}
			body = feed(app("up", "NewTitle"))
			if err := svc.SyncSource(context.Background(), "official"); err != nil {
				t.Fatalf("resync: %v", err)
			}
			_, b2 := get(t, srv, "/apps/official/up")
			if !strings.Contains(b2, "NewTitle") {
				t.Error("re-sync did not update app title")
			}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, c.fn)
	}
}
