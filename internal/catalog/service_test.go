package catalog

import (
	"strings"
	"testing"
	"unicode/utf8"
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

// TestNormalizeFeedAppExplanation covers how the integration-score explanation
// is normalized when ingesting a feed app.
func TestNormalizeFeedAppExplanation(t *testing.T) {
	const repo = "https://example.invalid/app"

	t.Run("explanation is trimmed and preserved when scored", func(t *testing.T) {
		got, ok := normalizeFeedApp("s", sourceFeedApp{
			Name: "app", Title: "App", RepoURL: repo,
			OpenhostIntegrationScore:            4,
			OpenhostIntegrationScoreExplanation: "  Owner is auto-logged in.  ",
		})
		if !ok {
			t.Fatal("expected app to normalize")
		}
		if got.OpenhostIntegrationScoreExplanation != "Owner is auto-logged in." {
			t.Errorf("explanation: got %q, want trimmed value", got.OpenhostIntegrationScoreExplanation)
		}
	})

	t.Run("explanation is dropped on an unrated app", func(t *testing.T) {
		got, _ := normalizeFeedApp("s", sourceFeedApp{
			Name: "app", Title: "App", RepoURL: repo,
			OpenhostIntegrationScore:            0,
			OpenhostIntegrationScoreExplanation: "orphan explanation with no score",
		})
		if got.OpenhostIntegrationScoreExplanation != "" {
			t.Errorf("explanation: got %q, want empty for unrated app", got.OpenhostIntegrationScoreExplanation)
		}
	})

	t.Run("overly long explanation is clamped on a rune boundary", func(t *testing.T) {
		// Use a multi-byte rune so a byte-boundary truncation would corrupt UTF-8.
		long := strings.Repeat("é", maxScoreExplanationLen+50)
		got, _ := normalizeFeedApp("s", sourceFeedApp{
			Name: "app", Title: "App", RepoURL: repo,
			OpenhostIntegrationScore:            3,
			OpenhostIntegrationScoreExplanation: long,
		})
		if n := utf8.RuneCountInString(got.OpenhostIntegrationScoreExplanation); n != maxScoreExplanationLen {
			t.Errorf("explanation rune count: got %d, want %d", n, maxScoreExplanationLen)
		}
		if !utf8.ValidString(got.OpenhostIntegrationScoreExplanation) {
			t.Error("explanation is not valid UTF-8 after clamping")
		}
	})
}

// TestClampRunes exercises the rune-boundary truncation helper directly.
func TestClampRunes(t *testing.T) {
	if got := clampRunes("hello", 0); got != "" {
		t.Errorf("clampRunes(_, 0): got %q, want empty", got)
	}
	if got := clampRunes("hello", 10); got != "hello" {
		t.Errorf("clampRunes under limit: got %q, want %q", got, "hello")
	}
	if got := clampRunes("héllo", 3); got != "hél" {
		t.Errorf("clampRunes multibyte: got %q, want %q", got, "hél")
	}
	if got := clampRunes("héllo", 3); !utf8.ValidString(got) {
		t.Error("clampRunes produced invalid UTF-8")
	}
}
