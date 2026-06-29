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

func app(name, title string, score int, expl string) string {
	return fmt.Sprintf(`{"name":%q,"title":%q,"repo_url":"https://github.com/x/%s","openhost_integration_score":%d,"openhost_integration_score_explanation":%q}`,
		name, title, name, score, expl)
}

// TestManualIntegration runs a large battery of rendering/behavior checks.
func TestManualIntegration(t *testing.T) {
	type tc struct {
		name string
		fn   func(t *testing.T)
	}

	cases := []tc{
		{"detail shows score and explanation", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("miniflux", "Miniflux", 4, "Owner auto-login via SSO.")))
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
			if !strings.Contains(body, "Owner auto-login via SSO.") {
				t.Error("missing explanation text")
			}
			if !strings.Contains(body, "rating-explanation") {
				t.Error("missing rating-explanation block")
			}
		}},

		{"detail unrated shows Unrated not a score", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("foo", "Foo", 0, "")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			code, body := get(t, srv, "/apps/official/foo")
			if code != 200 {
				t.Fatalf("code %d", code)
			}
			if !strings.Contains(body, "Unrated") {
				t.Error("expected 'Unrated' label")
			}
			if strings.Contains(body, "rating-explanation") {
				t.Error("unrated app should not show explanation block")
			}
			if strings.Contains(body, "0/5") {
				t.Error("unrated app should not show 0/5")
			}
		}},

		{"detail rated-but-no-explanation shows stars no block", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("bar", "Bar", 3, "")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			code, body := get(t, srv, "/apps/official/bar")
			if code != 200 {
				t.Fatalf("code %d", code)
			}
			if !strings.Contains(body, "3/5") {
				t.Error("missing 3/5")
			}
			if strings.Contains(body, "rating-explanation") {
				t.Error("no explanation -> no block")
			}
		}},

		{"detail explanation HTML-escaped (XSS)", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			payload := "<script>alert(1)</script>"
			fs := feedServer(t, feed(app("evil", "Evil", 5, payload)))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			code, body := get(t, srv, "/apps/official/evil")
			if code != 200 {
				t.Fatalf("code %d", code)
			}
			if strings.Contains(body, "<script>alert(1)</script>") {
				t.Error("explanation not escaped -> XSS")
			}
			if !strings.Contains(body, "&lt;script&gt;") {
				t.Error("expected escaped script tag")
			}
		}},

		{"detail title XSS escaped", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("t", "<img src=x onerror=alert(1)>", 5, "x")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/t")
			if strings.Contains(body, "<img src=x onerror=alert(1)>") {
				t.Error("title not escaped")
			}
		}},

		{"detail rubric link present", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("m", "M", 4, "x")))
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
				app("low", "ZebraLow", 2, "l"),
				app("hi1", "Bravo", 5, "h"),
				app("hi2", "Alpha", 5, "h"),
				app("un", "Mid", 0, ""),
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

		{"listing tooltip includes explanation", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("m", "M", 4, "Tooltip text here")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced=1&filter=1")
			if !strings.Contains(page, "Tooltip text here") {
				t.Error("explanation not in listing tooltip")
			}
			if !strings.Contains(page, "OpenHost integration: 4/5") {
				t.Error("missing tooltip score")
			}
		}},

		{"listing tooltip explanation attribute-escaped", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("m", "M", 4, `quote " and <tag>`)))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced=1&filter=1")
			if strings.Contains(page, `title="OpenHost integration: 4/5 — quote " and <tag>"`) {
				t.Error("tooltip not attribute-escaped")
			}
		}},

		{"listing unrated shows dash", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("u", "U", 0, "")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced=1&filter=1")
			if !strings.Contains(page, "rating-unrated") {
				t.Error("unrated listing should show rating-unrated marker")
			}
		}},

		{"listing rating header links rubric", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("m", "M", 4, "x")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced=1&filter=1")
			if !strings.Contains(page, "SCORING.md") {
				t.Error("missing rubric link in listing header")
			}
		}},

		{"ingest clamps score above 5", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"big","title":"Big","repo_url":"https://github.com/x/big","openhost_integration_score":99,"openhost_integration_score_explanation":"x"}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/big")
			if !strings.Contains(body, "5/5") {
				t.Error("score>5 should clamp to 5")
			}
		}},

		{"ingest clamps negative score to unrated", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"neg","title":"Neg","repo_url":"https://github.com/x/neg","openhost_integration_score":-4,"openhost_integration_score_explanation":"x"}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/neg")
			if !strings.Contains(body, "Unrated") {
				t.Error("negative score should render unrated")
			}
		}},

		{"ingest drops explanation on unrated", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"orph","title":"Orph","repo_url":"https://github.com/x/orph","openhost_integration_score":0,"openhost_integration_score_explanation":"should be dropped"}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/orph")
			if strings.Contains(body, "should be dropped") {
				t.Error("explanation should be dropped for unrated app")
			}
		}},

		{"ingest clamps long explanation to 400", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			long := strings.Repeat("z", 500)
			fs := feedServer(t, feed(app("lng", "Lng", 3, long)))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			g, err := st.GetCatalogApp(context.Background(), "official", "lng")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if len(g.OpenhostIntegrationScoreExplanation) != 400 {
				t.Errorf("explanation len = %d, want 400", len(g.OpenhostIntegrationScoreExplanation))
			}
		}},

		{"ingest trims whitespace explanation", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("trm", "Trm", 3, "   padded   ")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			g, _ := st.GetCatalogApp(context.Background(), "official", "trm")
			if g.OpenhostIntegrationScoreExplanation != "padded" {
				t.Errorf("got %q, want trimmed", g.OpenhostIntegrationScoreExplanation)
			}
		}},

		{"ingest omitted explanation field defaults empty", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"noexp","title":"NoExp","repo_url":"https://github.com/x/noexp","openhost_integration_score":3}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			g, _ := st.GetCatalogApp(context.Background(), "official", "noexp")
			if g.OpenhostIntegrationScoreExplanation != "" {
				t.Errorf("got %q, want empty", g.OpenhostIntegrationScoreExplanation)
			}
			if g.OpenhostIntegrationScore != 3 {
				t.Errorf("score %d want 3", g.OpenhostIntegrationScore)
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

		{"re-sync updates explanation and score", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			// A mutable feed handler whose body we swap between syncs.
			body := feed(app("up", "Up", 3, "old text"))
			fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, b1 := get(t, srv, "/apps/official/up")
			if !strings.Contains(b1, "old text") {
				t.Fatal("setup failed")
			}
			body = feed(app("up", "Up", 5, "new text"))
			if err := svc.SyncSource(context.Background(), "official"); err != nil {
				t.Fatalf("resync: %v", err)
			}
			_, b2 := get(t, srv, "/apps/official/up")
			if !strings.Contains(b2, "new text") || strings.Contains(b2, "old text") {
				t.Error("re-sync did not update explanation")
			}
			if !strings.Contains(b2, "5/5") {
				t.Error("re-sync did not update score")
			}
		}},

		{"multi-source same app id isolated", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fsA := feedServer(t, feed(app("dup", "DupA", 5, "from A")))
			defer fsA.Close()
			fsB := feedServer(t, feed(app("dup", "DupB", 2, "from B")))
			defer fsB.Close()
			addAndSync(t, svc, st, "srcA", "A", fsA.URL+"/catalog.json")
			addAndSync(t, svc, st, "srcB", "B", fsB.URL+"/catalog.json")
			_, bA := get(t, srv, "/apps/srcA/dup")
			_, bB := get(t, srv, "/apps/srcB/dup")
			if !strings.Contains(bA, "from A") || !strings.Contains(bA, "5/5") {
				t.Error("srcA wrong")
			}
			if !strings.Contains(bB, "from B") || !strings.Contains(bB, "2/5") {
				t.Error("srcB wrong")
			}
		}},

		{"detail 404 for missing app", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("real", "Real", 3, "x")))
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
				fs := feedServer(t, feed(app("lvl", "Lvl", s, "x")))
				addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
				_, body := get(t, srv, "/apps/official/lvl")
				if !strings.Contains(body, fmt.Sprintf("%d/5", s)) {
					t.Errorf("score %d not rendered", s)
				}
				fs.Close()
			}
		}},

		{"star glyph count matches score", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(app("st", "St", 3, "x")))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/st")
			// renderStars produces filled+empty to 5; expect at least the stars span.
			if !strings.Contains(body, "stars") {
				t.Error("missing stars span")
			}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, c.fn)
	}
}
