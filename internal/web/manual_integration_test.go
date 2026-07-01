package web

import (
	"context"
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
// used to sync. AllowHTTPRepoURLs/AllowFileRepoURLs aren't needed; the source
// URL is the httptest server.
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

func app(name, title string, score int) string {
	return fmt.Sprintf(`{"name":%q,"title":%q,"repo_url":"https://github.com/x/%s","openhost_integration_score":%d}`,
		name, title, name, score)
}

// TestManualIntegration runs a battery of rendering/behavior checks.
func TestManualIntegration(t *testing.T) {
	type tc struct {
		name string
		fn   func(t *testing.T)
	}

	cases := []tc{
		{"detail shows score", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("miniflux", "Miniflux", 4)))
			defer fs.Close()
			if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
				t.Fatalf("sync: %v", err)
			}
			code, body := get(t, srv, "/apps/official/miniflux")
			if code != 200 {
				t.Fatalf("code %d", code)
			}
			if !strings.Contains(body, "4/5") {
				t.Error("missing 4/5")
			}
			if !strings.Contains(body, "stars") {
				t.Error("missing stars span")
			}
		}},

		{"detail unrated shows Unrated not a score", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("foo", "Foo", 0)))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			code, body := get(t, srv, "/apps/official/foo")
			if code != 200 {
				t.Fatalf("code %d", code)
			}
			if !strings.Contains(body, "Unrated") {
				t.Error("expected 'Unrated' label")
			}
			if strings.Contains(body, "0/5") {
				t.Error("unrated app should not show 0/5")
			}
		}},

		{"detail title XSS escaped", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"t","title":"<img src=x onerror=alert(1)>","repo_url":"https://github.com/x/t","openhost_integration_score":5}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/t")
			if strings.Contains(body, "<img src=x onerror=alert(1)>") {
				t.Error("title not escaped")
			}
		}},

		{"detail rubric link present", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("m", "M", 4)))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/m")
			if !strings.Contains(body, "SCORING.md") {
				t.Error("missing rubric link on detail page")
			}
		}},

		{"listing sorts by score desc then title", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				app("low", "ZebraLow", 2),
				app("hi1", "Bravo", 5),
				app("hi2", "Alpha", 5),
				app("un", "Mid", 0),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced=1&filter=1")
			iAlpha := strings.Index(page, "Alpha")
			iBravo := strings.Index(page, "Bravo")
			iZebra := strings.Index(page, "ZebraLow")
			iMid := strings.Index(page, "Mid")
			if !(iAlpha < iBravo && iBravo < iZebra && iZebra < iMid) {
				t.Errorf("bad order: Alpha=%d Bravo=%d Zebra=%d Mid=%d", iAlpha, iBravo, iZebra, iMid)
			}
		}},

		{"listing tooltip shows score", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("m", "M", 4)))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced=1&filter=1")
			if !strings.Contains(page, "OpenHost integration: 4/5") {
				t.Error("missing tooltip score")
			}
		}},

		{"listing unrated shows dash", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("u", "U", 0)))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced=1&filter=1")
			if !strings.Contains(page, "rating-unrated") {
				t.Error("unrated listing should show rating-unrated marker")
			}
		}},

		{"listing rating header links rubric", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("m", "M", 4)))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced=1&filter=1")
			if !strings.Contains(page, "SCORING.md") {
				t.Error("missing rubric link in listing header")
			}
		}},

		{"ingest clamps score above 5", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"big","title":"Big","repo_url":"https://github.com/x/big","openhost_integration_score":99}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/big")
			if !strings.Contains(body, "5/5") {
				t.Error("score>5 should clamp to 5")
			}
		}},

		{"ingest clamps negative score to unrated", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"neg","title":"Neg","repo_url":"https://github.com/x/neg","openhost_integration_score":-4}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/neg")
			if !strings.Contains(body, "Unrated") {
				t.Error("negative score should render unrated")
			}
		}},

		{"ingest omitted score field defaults unrated", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"nos","title":"NoS","repo_url":"https://github.com/x/nos"}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			g, _ := st.GetCatalogApp(context.Background(), "official", "nos")
			if g.OpenhostIntegrationScore != 0 {
				t.Errorf("score %d want 0", g.OpenhostIntegrationScore)
			}
		}},

		{"feed with JSON bool score is rejected entirely", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"b","title":"B","repo_url":"https://github.com/x/b","openhost_integration_score":true}`))
			defer fs.Close()
			err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			if err == nil {
				t.Error("expected sync to fail on bool score (type mismatch)")
			}
		}},

		{"feed with string score is rejected entirely", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"s","title":"S","repo_url":"https://github.com/x/s","openhost_integration_score":"4"}`))
			defer fs.Close()
			err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			if err == nil {
				t.Error("expected sync to fail on string score")
			}
		}},

		{"re-sync updates score", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(app("up", "Up", 3))
			fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, b1 := get(t, srv, "/apps/official/up")
			if !strings.Contains(b1, "3/5") {
				t.Fatal("setup failed")
			}
			body = feed(app("up", "Up", 5))
			if err := svc.SyncSource(context.Background(), "official"); err != nil {
				t.Fatalf("resync: %v", err)
			}
			_, b2 := get(t, srv, "/apps/official/up")
			if !strings.Contains(b2, "5/5") {
				t.Error("re-sync did not update score")
			}
		}},

		{"multi-source same app id isolated", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fsA := feedServer(t, feed(app("dup", "DupA", 5)))
			defer fsA.Close()
			fsB := feedServer(t, feed(app("dup", "DupB", 2)))
			defer fsB.Close()
			addAndSync(t, svc, st, "srcA", "A", fsA.URL+"/catalog.json")
			addAndSync(t, svc, st, "srcB", "B", fsB.URL+"/catalog.json")
			_, bA := get(t, srv, "/apps/srcA/dup")
			_, bB := get(t, srv, "/apps/srcB/dup")
			if !strings.Contains(bA, "5/5") {
				t.Error("srcA wrong")
			}
			if !strings.Contains(bB, "2/5") {
				t.Error("srcB wrong")
			}
		}},

		{"detail 404 for missing app", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("real", "Real", 3)))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			code, _ := get(t, srv, "/apps/official/doesnotexist")
			if code != 404 {
				t.Errorf("code %d want 404", code)
			}
		}},

		{"all 5 score levels render correct N/5", func(t *testing.T) {
			for s := 1; s <= 5; s++ {
				srv, svc, st := newTestServer(t)
				fs := feedServer(t, feed(app("lvl", "Lvl", s)))
				addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
				_, body := get(t, srv, "/apps/official/lvl")
				if !strings.Contains(body, fmt.Sprintf("%d/5", s)) {
					t.Errorf("score %d not rendered", s)
				}
				fs.Close()
			}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, c.fn)
	}
}
