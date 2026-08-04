package web

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBuildAppTOMLOmitsEmptyOptionals(t *testing.T) {
	got := buildAppTOML(appTOMLFields{
		Name:        "my-app",
		Title:       "My App",
		Description: `Cool "app"`,
		RepoURL:     "https://github.com/you/my-app",
		Tags:        []string{"a", "b"},
		Categories:  []string{"ai"},
	})
	want := `[app]
name = "my-app"
title = "My App"
description = "Cool \"app\""
repo_url = "https://github.com/you/my-app"
tags = ["a", "b"]
categories = ["ai"]
`
	if got != want {
		t.Fatalf("unexpected toml:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildAppTOMLIncludesOptionals(t *testing.T) {
	got := buildAppTOML(appTOMLFields{
		Name:       "x",
		Title:      "X",
		RepoURL:    "https://github.com/you/x",
		RepoRef:    "v1.2.0",
		IconURL:    "https://example.com/i.png",
		WebsiteURL: "https://x.example",
		DocsURL:    "https://x.example/docs",
	})
	for _, want := range []string{
		`repo_ref = "v1.2.0"`,
		`icon_url = "https://example.com/i.png"`,
		`website_url = "https://x.example"`,
		`docs_url = "https://x.example/docs"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("toml missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "tags =") || strings.Contains(got, "categories =") {
		t.Errorf("empty tags/categories should be omitted:\n%s", got)
	}
}

func TestBuildListingEntryValidation(t *testing.T) {
	tests := []struct {
		name     string
		toml     string
		wantName string // non-empty means expect success with this name
		wantErr  string // substring; "" means expect success
	}{
		{"valid", `[app]
name = "my-app"
title = "My App"
description = "d"
repo_url = "https://github.com/you/my-app"
categories = ["ai"]`, "my-app", ""},
		{"parse error", `[app] name = broken`, "", "Could not parse"},
		{"empty", "  \n ", "", "Paste your app.toml"},
		{"bad name", `[app]
name = "My_App"
title = "t"
description = "d"
repo_url = "https://github.com/you/x"`, "", "lowercase alphanumeric"},
		{"missing title", `[app]
name = "x"
description = "d"
repo_url = "https://github.com/you/x"`, "", "title is required"},
		{"missing repo", `[app]
name = "x"
title = "t"
description = "d"`, "", "repo_url is required"},
		{"bad repo scheme", `[app]
name = "x"
title = "t"
description = "d"
repo_url = "ftp://github.com/you/x"`, "", "GitHub repo"},
		{"non-github repo", `[app]
name = "x"
title = "t"
description = "d"
repo_url = "https://gitlab.com/you/x"`, "", "GitHub repo"},
		{"github no repo path", `[app]
name = "x"
title = "t"
description = "d"
repo_url = "https://github.com/you"`, "", "GitHub repo"},
		{"bad category", `[app]
name = "x"
title = "t"
description = "d"
repo_url = "https://github.com/you/x"
categories = ["bogus"]`, "", "Unknown categories"},
		{"bad icon", `[app]
name = "x"
title = "t"
description = "d"
repo_url = "https://github.com/you/x"
icon_url = "javascript:alert(1)"`, "", "icon_url"},
		{"ref with space", `[app]
name = "x"
title = "t"
description = "d"
repo_url = "https://github.com/you/x"
repo_ref = "a b"`, "", "whitespace"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, errs := buildListingEntry(tc.toml)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected no errors, got %v", errs)
				}
				if entry.name != tc.wantName || entry.toml == "" {
					t.Fatalf("expected populated entry named %q, got %+v", tc.wantName, entry)
				}
				return
			}
			if !containsSubstr(errs, tc.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", tc.wantErr, errs)
			}
		})
	}
}

func TestCandidateRefs(t *testing.T) {
	tests := []struct {
		repoRef, urlRef string
		want            []string
	}{
		{"", "", []string{"HEAD"}},
		{"v1", "", []string{"v1", "HEAD"}},
		{"", "main", []string{"main", "HEAD"}},
		{"v1", "main", []string{"v1", "main", "HEAD"}},
		{"HEAD", "HEAD", []string{"HEAD"}},
		{"main", "main", []string{"main", "HEAD"}},
	}
	for _, tc := range tests {
		got := candidateRefs(tc.repoRef, tc.urlRef)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("candidateRefs(%q, %q) = %v, want %v", tc.repoRef, tc.urlRef, got, tc.want)
		}
	}
}

func TestParseGitHubRepo(t *testing.T) {
	tests := []struct {
		raw              string
		owner, repo, ref string
		ok               bool
	}{
		{"https://github.com/you/my-app", "you", "my-app", "", true},
		{"https://www.github.com/you/my-app.git", "you", "my-app", "", true},
		{"https://github.com/you/my-app/tree/main", "you", "my-app", "main", true},
		{"https://github.com/you/my-app/tree/feature", "you", "my-app", "feature", true},
		{"https://gitlab.com/you/my-app", "", "", "", false},
		{"ftp://github.com/you/my-app", "", "", "", false},
		{"https://github.com/you", "", "", "", false},
		{"not a url", "", "", "", false},
	}
	for _, tc := range tests {
		owner, repo, ref, ok := parseGitHubRepo(tc.raw)
		if ok != tc.ok || owner != tc.owner || repo != tc.repo || ref != tc.ref {
			t.Errorf("parseGitHubRepo(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
				tc.raw, owner, repo, ref, ok, tc.owner, tc.repo, tc.ref, tc.ok)
		}
	}
}

func TestHostResolves(t *testing.T) {
	orig := lookupHost
	defer func() { lookupHost = orig }()
	lookupHost = func(_ context.Context, host string) ([]string, error) {
		if host == "good.example" {
			return []string{"1.2.3.4"}, nil
		}
		return nil, errors.New("no such host")
	}
	tests := []struct {
		rawURL string
		want   bool
	}{
		{"https://good.example/icon.png", true},
		{"https://good.example:8443/icon.png", true},
		{"https://bad.example/icon.png", false},
		{"not a url", false},
	}
	for _, tc := range tests {
		if got := hostResolves(context.Background(), tc.rawURL); got != tc.want {
			t.Errorf("hostResolves(%q) = %v, want %v", tc.rawURL, got, tc.want)
		}
	}
}

func TestBuildForkURL(t *testing.T) {
	got := buildForkURL("https://github.com/imbue-openhost/openhost-apps/")
	if want := "https://github.com/imbue-openhost/openhost-apps/fork"; got != want {
		t.Fatalf("fork url = %q, want %q", got, want)
	}
}

func containsSubstr(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
