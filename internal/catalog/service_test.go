package catalog

import (
	"encoding/json"
	"testing"
)

// TestNormalizeFeedAppScoreClamp covers how the integration score is
// normalized when ingesting a feed app.
func TestNormalizeFeedAppScoreClamp(t *testing.T) {
	const repo = "https://example.invalid/app"

	t.Run("in-range score is preserved", func(t *testing.T) {
		got, ok := normalizeFeedApp("s", sourceFeedApp{
			Name: "app", Title: "App", RepoURL: repo, OpenhostIntegrationScore: 4,
		})
		if !ok {
			t.Fatal("expected app to normalize")
		}
		if got.OpenhostIntegrationScore != 4 {
			t.Errorf("score: got %d, want 4", got.OpenhostIntegrationScore)
		}
	})

	t.Run("out-of-range score is clamped to 0-5", func(t *testing.T) {
		hi, _ := normalizeFeedApp("s", sourceFeedApp{Name: "a", Title: "A", RepoURL: repo, OpenhostIntegrationScore: 9})
		if hi.OpenhostIntegrationScore != 5 {
			t.Errorf("high score: got %d, want 5", hi.OpenhostIntegrationScore)
		}
		lo, _ := normalizeFeedApp("s", sourceFeedApp{Name: "a", Title: "A", RepoURL: repo, OpenhostIntegrationScore: -3})
		if lo.OpenhostIntegrationScore != 0 {
			t.Errorf("low score: got %d, want 0", lo.OpenhostIntegrationScore)
		}
	})
}

// TestNormalizeFeedAppFederationURL covers how the optional federation_url
// field is normalized when ingesting a feed app.
func TestNormalizeFeedAppFederationURL(t *testing.T) {
	const repo = "https://example.invalid/app"

	t.Run("valid https URL is preserved", func(t *testing.T) {
		got, ok := normalizeFeedApp("s", sourceFeedApp{
			Name: "app", Title: "App", RepoURL: repo,
			FederationURL: "https://spec.example/chat/v1",
		})
		if !ok {
			t.Fatal("expected app to normalize")
		}
		if got.FederationURL != "https://spec.example/chat/v1" {
			t.Errorf("federation url: got %q, want %q", got.FederationURL, "https://spec.example/chat/v1")
		}
	})

	t.Run("missing field yields empty string", func(t *testing.T) {
		got, _ := normalizeFeedApp("s", sourceFeedApp{Name: "app", Title: "App", RepoURL: repo})
		if got.FederationURL != "" {
			t.Errorf("federation url: got %q, want empty", got.FederationURL)
		}
	})

	t.Run("surrounding whitespace is trimmed", func(t *testing.T) {
		got, _ := normalizeFeedApp("s", sourceFeedApp{
			Name: "app", Title: "App", RepoURL: repo,
			FederationURL: "  https://spec.example/chat/v1  ",
		})
		if got.FederationURL != "https://spec.example/chat/v1" {
			t.Errorf("federation url: got %q, want trimmed URL", got.FederationURL)
		}
	})

	t.Run("non-http scheme is dropped", func(t *testing.T) {
		got, _ := normalizeFeedApp("s", sourceFeedApp{
			Name: "app", Title: "App", RepoURL: repo,
			FederationURL: "javascript:alert(1)",
		})
		if got.FederationURL != "" {
			t.Errorf("federation url: got %q, want empty for unsafe scheme", got.FederationURL)
		}
	})
}

// TestFeedParsesFederationURL verifies the feed JSON field name.
func TestFeedParsesFederationURL(t *testing.T) {
	body := `{
		"schema": "openhost.catalog.v1",
		"apps": [{
			"name": "chat",
			"title": "Chat",
			"repo_url": "https://example.invalid/chat",
			"federation_url": "https://spec.example/chat/v1"
		}]
	}`
	var feed sourceFeed
	if err := json.Unmarshal([]byte(body), &feed); err != nil {
		t.Fatalf("unmarshal feed: %v", err)
	}
	if len(feed.Apps) != 1 {
		t.Fatalf("want 1 app, got %d", len(feed.Apps))
	}
	if feed.Apps[0].FederationURL != "https://spec.example/chat/v1" {
		t.Errorf("federation url: got %q, want %q", feed.Apps[0].FederationURL, "https://spec.example/chat/v1")
	}
}
