package web

import (
	"fmt"
	"strings"
	"testing"
)

// TestAppDetailRendersScoreAndExplanation verifies the app detail page surfaces
// the OpenHost integration score, its N/5 label, and the human-readable
// explanation, and that the explanation is HTML-escaped.
func TestAppDetailRendersScoreAndExplanation(t *testing.T) {
	srv, svc, st := newTestServer(t)

	entry := fmt.Sprintf(
		`{"name":"rated","title":"Rated App","repo_url":"https://github.com/x/rated","openhost_integration_score":4,"openhost_integration_score_explanation":%q}`,
		`Owner auto-login works; guests are bounced <script>.`,
	)
	fs := feedServer(t, feed(entry))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	code, body := get(t, srv, "/apps/official/rated")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(body, "4/5") {
		t.Error("detail page should show the N/5 rating")
	}
	if !strings.Contains(body, "Owner auto-login works") {
		t.Error("detail page should render the score explanation")
	}
	// The explanation must be HTML-escaped, not injected as raw markup.
	if strings.Contains(body, "<script>") {
		t.Error("explanation was not HTML-escaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("explanation should render escaped angle brackets")
	}
	// The rubric link must be present.
	if !strings.Contains(body, "openhost-apps/blob/main/SCORING.md") {
		t.Error("detail page should link to the scoring rubric")
	}
}

// TestAppDetailUnratedHasNoExplanation verifies that an app with no score
// renders as "Unrated" and shows no explanation block.
func TestAppDetailUnratedHasNoExplanation(t *testing.T) {
	srv, svc, st := newTestServer(t)

	fs := feedServer(t, feed(app("plain", "Plain App")))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	code, body := get(t, srv, "/apps/official/plain")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(body, "Unrated") {
		t.Error("unrated app should render as Unrated")
	}
	if strings.Contains(body, "rating-explanation") {
		t.Error("unrated app should not render an explanation block")
	}
}
