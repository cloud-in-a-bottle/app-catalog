package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/imbue-openhost/openhost-catalog/internal/store"
)

// Reuses helpers from manual_integration_test.go in the same package:
// newTestServer, feedServer, addAndSync, get, feed, app.

func TestNicheIntegration(t *testing.T) {
	type tc struct {
		name string
		fn   func(t *testing.T)
	}
	cases := []tc{
		// --- Injection / escaping vectors via title ---
		{"title with template-injection braces escaped", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"ti","title":"{{.Secret}} ${jndi:ldap://x}","repo_url":"https://github.com/x/ti","openhost_integration_score":5}`))
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
			body := fmt.Sprintf(`{"name":"sqli","title":%q,"repo_url":"https://github.com/x/sqli","openhost_integration_score":4}`, payload)
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

		// --- Boundary scores ---
		{"score exactly 1 and 5 boundary render", func(t *testing.T) {
			for _, s := range []int{1, 5} {
				srv, svc, st := newTestServer(t)
				fs := feedServer(t, feed(app("b", "B", s)))
				addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
				_, body := get(t, srv, "/apps/official/b")
				if !strings.Contains(body, fmt.Sprintf("%d/5", s)) {
					t.Errorf("boundary score %d not rendered", s)
				}
				fs.Close()
			}
		}},

		{"score 1000000 clamps to 5", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"big","title":"Big","repo_url":"https://github.com/x/big","openhost_integration_score":1000000}`))
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, body := get(t, srv, "/apps/official/big")
			if !strings.Contains(body, "5/5") {
				t.Error("huge score should clamp to 5")
			}
		}},

		// --- Malformed feeds ---
		{"feed with float score rejected entirely", func(t *testing.T) {
			_, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"f","title":"F","repo_url":"https://github.com/x/f","openhost_integration_score":3.5}`))
			defer fs.Close()
			if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err == nil {
				t.Error("float score should fail JSON decode for int field")
			}
		}},

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
			body := feed(app("dup", "Dup1", 5) + "," + app("dup", "Dup2", 2))
			fs := feedServer(t, body)
			defer fs.Close()
			if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err == nil {
				t.Error("duplicate app id within a source should be rejected")
			}
		}},

		// --- Sort stability with ties ---
		{"equal scores sort by title case-insensitively", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(strings.Join([]string{
				app("c", "charlie", 4),
				app("a", "Alpha", 4),
				app("b", "bravo", 4),
			}, ","))
			fs := feedServer(t, body)
			defer fs.Close()
			addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
			_, page := get(t, srv, "/?advanced=1&filter=1")
			ia := strings.Index(page, "Alpha")
			ib := strings.Index(page, "bravo")
			ic := strings.Index(page, "charlie")
			if !(ia < ib && ib < ic) {
				t.Errorf("case-insensitive title sort failed: A=%d b=%d c=%d", ia, ib, ic)
			}
		}},

		// --- Multi-source serialized sync (how syncs run in production) ---
		{"many sources synced in sequence stay consistent", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			servers := make([]*httptest.Server, 5)
			for i := 0; i < 5; i++ {
				name := fmt.Sprintf("appc%d", i)
				fs := feedServer(t, feed(app(name, "App"+name, (i%5)+1)))
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
			_, page := get(t, srv, "/?advanced=1&filter=1")
			if !strings.Contains(page, "Appappc0") {
				t.Error("synced app missing from listing")
			}
		}},

		// --- Concurrent READS during repeated syncs stay safe ---
		{"concurrent reads during repeated syncs never error", func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			body := feed(app("rc", "RC", 4))
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
						if code, body := get(t, srv, "/apps/official/rc"); code != 200 {
							readErr <- fmt.Sprintf("detail code %d", code)
						} else if !strings.Contains(body, "4/5") {
							readErr <- "detail missing score during sync"
						}
						if code, _ := get(t, srv, "/?advanced=1&filter=1"); code != 200 {
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
