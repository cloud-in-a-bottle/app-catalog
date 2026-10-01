package web

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestCatalogPagesShowIndependentLicenses(t *testing.T) {
	for _, tc := range []struct{ name, fields, license, packaging string }{
		{"distinct", `,"license":" Apache-2.0 ","packaging_license":" MIT "`, "Apache-2.0", "MIT"},
		{"application-only", `,"license":"MIT OR Apache-2.0"`, "MIT OR Apache-2.0", "Not specified"},
		{"packaging-only", `,"packaging_license":"LicenseRef-Custom"`, "Not specified", "LicenseRef-Custom"},
		{"legacy", ``, "Not specified", "Not specified"},
		{"null-application", `,"license":null,"packaging_license":"MIT"`, "Not specified", "MIT"},
		{"null-packaging", `,"license":"Apache-2.0","packaging_license":null`, "Apache-2.0", "Not specified"},
		{"blank", `,"license":" \t ","packaging_license":""`, "Not specified", "Not specified"},
		{"html", `,"license":"<script>alert(1)</script>","packaging_license":"<img src=x onerror=alert(1)>"`, "<script>alert(1)</script>", "<img src=x onerror=alert(1)>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			fs := feedServer(t, feed(`{"name":"licensed","title":"Licensed app","repo_url":"https://github.com/example/app"`+tc.fields+`}`))
			defer fs.Close()
			if err := addAndSync(t, svc, st, "s", "Source", fs.URL); err != nil {
				t.Fatal(err)
			}
			for _, route := range []string{"/?category=all", "/apps/s/licensed"} {
				code, body := get(t, srv, route)
				if code != http.StatusOK {
					t.Fatalf("%s: HTTP %d", route, code)
				}
				for _, field := range []struct{ label, value string }{{"Application license", tc.license}, {"Packaging license", tc.packaging}} {
					want := field.label + ": " + html.EscapeString(field.value) + "</div>"
					if route == "/apps/s/licensed" {
						want = `<th scope="row">` + field.label + `</th><td class="wrap-any">` + html.EscapeString(field.value) + `</td>`
					}
					if !strings.Contains(body, want) {
						t.Errorf("%s: missing %q", route, want)
					}
				}
				if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, "<img src=x onerror=alert(1)>") {
					t.Errorf("%s rendered license HTML unsafely", route)
				}
			}
		})
	}
}

func TestLicenseSyncPreservesCacheOnInvalidTypesAndClearsRemovedValues(t *testing.T) {
	_, svc, st := newTestServer(t)
	var body atomic.Value
	body.Store(feed(`{"name":"a","title":"A","repo_url":"https://github.com/example/a","license":"Apache-2.0","packaging_license":"MIT"}`))
	fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "s", "Source", fs.URL); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, field := range []string{"license", "packaging_license"} {
		for _, value := range []string{`true`, `42`, `["MIT"]`, `{"name":"MIT"}`} {
			body.Store(feed(`{"name":"a","title":"A","repo_url":"https://github.com/example/a","` + field + `":` + value + `}`))
			if err := svc.SyncSource(ctx, "s"); err == nil {
				t.Fatalf("accepted non-string %s: %s", field, value)
			}
			app, err := st.GetCatalogApp(ctx, "s", "a")
			if err != nil {
				t.Fatal(err)
			}
			if app.License != "Apache-2.0" || app.PackagingLicense != "MIT" {
				t.Fatalf("failed sync changed cached licenses: %+v", app)
			}
		}
	}
	for _, removed := range []string{"", `,"license":null,"packaging_license":null`} {
		body.Store(feed(`{"name":"a","title":"A","repo_url":"https://github.com/example/a","license":"BSD-3-Clause","packaging_license":"Apache-2.0"}`))
		if err := svc.SyncSource(ctx, "s"); err != nil {
			t.Fatal(err)
		}
		app, err := st.GetCatalogApp(ctx, "s", "a")
		if err != nil {
			t.Fatal(err)
		}
		if app.License != "BSD-3-Clause" || app.PackagingLicense != "Apache-2.0" {
			t.Fatalf("licenses did not refresh: %+v", app)
		}
		body.Store(feed(`{"name":"a","title":"A","repo_url":"https://github.com/example/a"` + removed + `}`))
		if err := svc.SyncSource(ctx, "s"); err != nil {
			t.Fatal(err)
		}
		app, err = st.GetCatalogApp(ctx, "s", "a")
		if err != nil {
			t.Fatal(err)
		}
		if app.License != "" || app.PackagingLicense != "" {
			t.Fatalf("unspecified licenses remain cached: %+v", app)
		}
	}
}

func TestListingSubmissionPreservesLicenseMetadata(t *testing.T) {
	const base = "[app]\nname = \"example\"\ntitle = \"Example\"\ndescription = \"Example app\"\nrepo_url = \"https://github.com/example/app\"\n"
	for _, tc := range []struct{ fields, license, packaging string }{
		{`license = " Apache-2.0 "
packaging_license = " MIT "`, "Apache-2.0", "MIT"},
		{`license = "MIT OR Apache-2.0"`, "MIT OR Apache-2.0", ""},
		{`packaging_license = 'Custom "license" \ terms'`, "", `Custom "license" \ terms`},
		{`license = " "
packaging_license = ""`, "", ""},
	} {
		entry, errs := buildListingEntry(base + tc.fields)
		if len(errs) != 0 {
			t.Fatalf("submission errors: %v", errs)
		}
		var decoded appManifest
		if _, err := toml.Decode(entry.toml, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.App.License != tc.license || decoded.App.PackagingLicense != tc.packaging {
			t.Fatalf("licenses did not round-trip: %s", entry.toml)
		}
		if tc.license == "" && strings.Contains("\n"+entry.toml, "\nlicense =") {
			t.Fatal("empty application license was not omitted")
		}
		if tc.packaging == "" && strings.Contains(entry.toml, "packaging_license =") {
			t.Fatal("empty packaging license was not omitted")
		}
	}
	for _, field := range []string{"license", "packaging_license"} {
		for _, value := range []string{`true`, `42`, `["MIT"]`, `{name = "MIT"}`} {
			if _, errs := buildListingEntry(base + field + " = " + value); len(errs) == 0 {
				t.Fatalf("accepted non-string %s: %s", field, value)
			}
		}
	}
}
