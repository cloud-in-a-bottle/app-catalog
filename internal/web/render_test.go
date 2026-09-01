package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloud-in-a-bottle/app-catalog/internal/store"
)

// Executes every page template; component defines invoked via dict fail only
// at execution time, so parsing alone does not prove the pages render.
func TestAllPagesRender(t *testing.T) {
	srv, svc, st := newTestServer(t)
	fs := feedServer(t, feed(`{"name":"a","title":"App A","repo_url":"https://github.com/x/a","tags":["rss"],"categories":["privacy"]}`))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := st.CreatePublish(context.Background(), store.Publish{
		ID: "pub1", SourceID: "official", AppID: "a", Title: "App A",
		RepoURL: "https://github.com/x/a", RouterAppName: "a", Status: "pending",
	}); err != nil {
		t.Fatalf("create publish: %v", err)
	}

	pages := map[string]string{
		"/":                       `class="banner"`,
		"/?category=all":          `class="cat-banner"`,
		"/?advanced&filter=1&q=a": `class="adv-form"`,
		"/apps/official/a":        `class="info-table"`,
		"/sources":                `Configured Sources`,
		"/submit":                 `class="submit-form"`,
		"/publishes/pub1":         `id="publish-status"`,
	}
	const faviconLink = `rel="icon" type="image/svg+xml" href="/static/img/favicon.svg"`
	for path, want := range pages {
		code, body := get(t, srv, path)
		if code != 200 {
			t.Errorf("%s: code %d", path, code)
			continue
		}
		if !strings.Contains(body, want) {
			t.Errorf("%s: missing %q", path, want)
		}
		if strings.Contains(body, "no value") {
			t.Errorf("%s: template printed a missing value", path)
		}
		if !strings.Contains(body, faviconLink) {
			t.Errorf("%s: missing favicon link", path)
		}
	}
}

func TestFaviconServed(t *testing.T) {
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/static/img/favicon.svg", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("favicon: code %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Fatalf("favicon: content type %q", got)
	}
	if !strings.Contains(rec.Body.String(), `viewBox="0 0 26 18"`) {
		t.Fatal("favicon: missing expected viewBox")
	}
}

func TestFaviconLinkIncludesAppBasePath(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.cfg.AppBasePath = "/catalog"
	req := httptest.NewRequest(http.MethodGet, "http://zone.example/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("index: code %d", rec.Code)
	}
	const want = `rel="icon" type="image/svg+xml" href="/catalog/static/img/favicon.svg"`
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("index: missing base-path favicon link %q", want)
	}
}
