package catalog

import (
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
