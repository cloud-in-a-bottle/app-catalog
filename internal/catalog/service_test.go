package catalog

import (
	"strings"
	"testing"
)

// TestNormalizeFeedAppScoreExplanation covers how the integration-score
// explanation is normalized when ingesting a feed app.
func TestNormalizeFeedAppScoreExplanation(t *testing.T) {
	const repo = "https://example.invalid/app"

	t.Run("preserved and trimmed for a rated app", func(t *testing.T) {
		got, ok := normalizeFeedApp("s", sourceFeedApp{
			Name:                                "app",
			Title:                               "App",
			RepoURL:                             repo,
			OpenhostIntegrationScore:            4,
			OpenhostIntegrationScoreExplanation: "  Owner is auto-logged in.  ",
		})
		if !ok {
			t.Fatal("expected app to normalize")
		}
		if got.OpenhostIntegrationScore != 4 {
			t.Errorf("score: got %d, want 4", got.OpenhostIntegrationScore)
		}
		if got.OpenhostIntegrationScoreExplanation != "Owner is auto-logged in." {
			t.Errorf("explanation: got %q, want trimmed text", got.OpenhostIntegrationScoreExplanation)
		}
	})

	t.Run("dropped for an unrated app", func(t *testing.T) {
		got, ok := normalizeFeedApp("s", sourceFeedApp{
			Name:                                "app",
			Title:                               "App",
			RepoURL:                             repo,
			OpenhostIntegrationScore:            0,
			OpenhostIntegrationScoreExplanation: "orphan explanation",
		})
		if !ok {
			t.Fatal("expected app to normalize")
		}
		if got.OpenhostIntegrationScoreExplanation != "" {
			t.Errorf("explanation: got %q, want empty for unrated app", got.OpenhostIntegrationScoreExplanation)
		}
	})

	t.Run("clamped when too long", func(t *testing.T) {
		long := strings.Repeat("x", maxScoreExplanationLen+50)
		got, ok := normalizeFeedApp("s", sourceFeedApp{
			Name:                                "app",
			Title:                               "App",
			RepoURL:                             repo,
			OpenhostIntegrationScore:            3,
			OpenhostIntegrationScoreExplanation: long,
		})
		if !ok {
			t.Fatal("expected app to normalize")
		}
		if len(got.OpenhostIntegrationScoreExplanation) != maxScoreExplanationLen {
			t.Errorf("explanation length: got %d, want %d", len(got.OpenhostIntegrationScoreExplanation), maxScoreExplanationLen)
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
