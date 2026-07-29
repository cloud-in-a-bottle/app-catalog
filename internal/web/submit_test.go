package web

import (
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
		name    string
		form    submitForm
		wantErr string // substring; "" means expect success
	}{
		{"valid", submitForm{Name: "my-app", Title: "My App", Description: "d", RepoURL: "https://github.com/you/my-app", Categories: []string{"ai"}}, ""},
		{"bad name", submitForm{Name: "My_App", Title: "t", Description: "d", RepoURL: "https://github.com/you/x"}, "lowercase alphanumeric"},
		{"missing title", submitForm{Name: "x", Description: "d", RepoURL: "https://github.com/you/x"}, "Title is required"},
		{"missing repo", submitForm{Name: "x", Title: "t", Description: "d"}, "Repository URL is required"},
		{"bad repo scheme", submitForm{Name: "x", Title: "t", Description: "d", RepoURL: "ftp://h/x"}, "absolute http(s) URL"},
		{"non-github repo", submitForm{Name: "x", Title: "t", Description: "d", RepoURL: "https://gitlab.com/you/x"}, "GitHub repo"},
		{"github no repo path", submitForm{Name: "x", Title: "t", Description: "d", RepoURL: "https://github.com/you"}, "GitHub repo"},
		{"bad category", submitForm{Name: "x", Title: "t", Description: "d", RepoURL: "https://github.com/you/x", Categories: []string{"bogus"}}, "Unknown categories"},
		{"bad icon", submitForm{Name: "x", Title: "t", Description: "d", RepoURL: "https://github.com/you/x", IconURL: "javascript:alert(1)"}, "Icon URL"},
		{"ref with space", submitForm{Name: "x", Title: "t", Description: "d", RepoURL: "https://github.com/you/x", RepoRef: "a b"}, "whitespace"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, errs := buildListingEntry(tc.form)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected no errors, got %v", errs)
				}
				if entry.name != tc.form.Name || entry.toml == "" {
					t.Fatalf("expected populated entry, got %+v", entry)
				}
				return
			}
			if !containsSubstr(errs, tc.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", tc.wantErr, errs)
			}
		})
	}
}

func TestParseGitHubRepo(t *testing.T) {
	tests := []struct {
		raw         string
		owner, repo string
		ok          bool
	}{
		{"https://github.com/you/my-app", "you", "my-app", true},
		{"https://www.github.com/you/my-app.git", "you", "my-app", true},
		{"https://github.com/you/my-app/tree/main", "you", "my-app", true},
		{"https://gitlab.com/you/my-app", "", "", false},
		{"https://github.com/you", "", "", false},
		{"not a url", "", "", false},
	}
	for _, tc := range tests {
		owner, repo, ok := parseGitHubRepo(tc.raw)
		if ok != tc.ok || owner != tc.owner || repo != tc.repo {
			t.Errorf("parseGitHubRepo(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.raw, owner, repo, ok, tc.owner, tc.repo, tc.ok)
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
