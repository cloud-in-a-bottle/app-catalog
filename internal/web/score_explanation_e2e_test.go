package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/imbue-openhost/openhost-catalog/internal/store"
)

// This file contains extensive end-to-end coverage for the OpenHost integration
// score and its explanation: feed ingest -> store -> HTTP render. Tests drive
// the real HTTP handlers through a live in-process server backed by a temp DB,
// exercising the same path a browser would.

// appScored builds a feed entry carrying a score and explanation.
func appScored(name, title string, score int, explanation string) string {
	return fmt.Sprintf(
		`{"name":%q,"title":%q,"repo_url":"https://github.com/x/%s","openhost_integration_score":%d,"openhost_integration_score_explanation":%q}`,
		name, title, name, score, explanation,
	)
}

// appScoredRaw builds a feed entry where the score is injected as a raw JSON
// token, so tests can supply non-integer or out-of-range values.
func appScoredRaw(name, title, scoreToken, explanation string) string {
	return fmt.Sprintf(
		`{"name":%q,"title":%q,"repo_url":"https://github.com/x/%s","openhost_integration_score":%s,"openhost_integration_score_explanation":%q}`,
		name, title, name, scoreToken, explanation,
	)
}

// detailRow returns the rendered "OpenHost integration" table cell for an app
// detail page, or "" if the page could not be fetched.
func detailRow(t *testing.T, srv *Server, sourceID, appID string) string {
	t.Helper()
	code, body := get(t, srv, "/apps/"+sourceID+"/"+appID)
	if code != 200 {
		t.Fatalf("detail page %s/%s: code %d", sourceID, appID, code)
	}
	const marker = "OpenHost integration"
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	end := strings.Index(body[i:], "</tr>")
	if end < 0 {
		return body[i:]
	}
	return body[i : i+end]
}

// mutableFeedServer is an httptest server whose /catalog.json body can be
// swapped between syncs, to exercise re-sync behavior.
type mutableFeedServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	body string
}

func newMutableFeedServer(t *testing.T, body string) *mutableFeedServer {
	t.Helper()
	m := &mutableFeedServer{body: body}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		b := m.body
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mutableFeedServer) set(body string) {
	m.mu.Lock()
	m.body = body
	m.mu.Unlock()
}

// TestE2EScoreRendersAllTiers confirms every score tier 1-5 renders the right
// number of filled stars, the N/5 label, and the explanation.
func TestE2EScoreRendersAllTiers(t *testing.T) {
	srv, svc, st := newTestServer(t)

	entries := make([]string, 0, 5)
	for s := 1; s <= 5; s++ {
		entries = append(entries, appScored(
			fmt.Sprintf("app%d", s),
			fmt.Sprintf("App %d", s),
			s,
			fmt.Sprintf("Tier %d explanation.", s),
		))
	}
	fs := feedServer(t, feed(strings.Join(entries, ",")))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	for s := 1; s <= 5; s++ {
		row := detailRow(t, srv, "official", fmt.Sprintf("app%d", s))
		filled := strings.Count(row, "\u2605")
		hollow := strings.Count(row, "\u2606")
		if filled != s {
			t.Errorf("app%d: got %d filled stars, want %d (row=%q)", s, filled, s, row)
		}
		if filled+hollow != 5 {
			t.Errorf("app%d: total stars %d, want 5", s, filled+hollow)
		}
		if !strings.Contains(row, fmt.Sprintf("%d/5", s)) {
			t.Errorf("app%d: missing N/5 label", s)
		}
		if !strings.Contains(row, fmt.Sprintf("Tier %d explanation.", s)) {
			t.Errorf("app%d: missing explanation", s)
		}
		if !strings.Contains(row, "rating-explanation") {
			t.Errorf("app%d: missing rating-explanation block", s)
		}
	}
}

// TestE2ERubricLinkPresent confirms the detail page links the scoring rubric.
func TestE2ERubricLinkPresent(t *testing.T) {
	srv, svc, st := newTestServer(t)
	fs := feedServer(t, feed(appScored("r", "R", 4, "ex")))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	_, body := get(t, srv, "/apps/official/r")
	if !strings.Contains(body, "openhost-apps/blob/main/SCORING.md") {
		t.Error("detail page missing scoring rubric link")
	}
}

// TestE2EUnratedRendersNoExplanation covers apps with an omitted score.
func TestE2EUnratedRendersNoExplanation(t *testing.T) {
	srv, svc, st := newTestServer(t)
	fs := feedServer(t, feed(app("plain", "Plain")))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	row := detailRow(t, srv, "official", "plain")
	if !strings.Contains(row, "Unrated") {
		t.Errorf("unrated app should render Unrated, got %q", row)
	}
	if strings.Contains(row, "rating-explanation") {
		t.Error("unrated app should not render an explanation block")
	}
	if strings.Contains(row, "\u2605") {
		t.Error("unrated app should render no filled stars")
	}
}

// TestE2EExplanationWithoutScoreIsDropped verifies that an explanation supplied
// without a score (score omitted/0) is dropped end-to-end and never rendered.
func TestE2EExplanationWithoutScoreIsDropped(t *testing.T) {
	srv, svc, st := newTestServer(t)
	entry := `{"name":"orphan","title":"Orphan","repo_url":"https://github.com/x/orphan","openhost_integration_score_explanation":"should never appear"}`
	fs := feedServer(t, feed(entry))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	got, err := st.GetCatalogApp(context.Background(), "official", "orphan")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OpenhostIntegrationScoreExplanation != "" {
		t.Errorf("stored explanation should be empty, got %q", got.OpenhostIntegrationScoreExplanation)
	}
	row := detailRow(t, srv, "official", "orphan")
	if strings.Contains(row, "should never appear") {
		t.Error("orphan explanation leaked into the detail page")
	}
	if strings.Contains(row, "rating-explanation") {
		t.Error("orphan explanation rendered a block")
	}
}

// TestE2EScoreClampingViaFeed exercises out-of-range score tokens straight from
// the JSON feed, through ingest, to render.
func TestE2EScoreClampingViaFeed(t *testing.T) {
	cases := []struct {
		name       string
		scoreToken string
		wantStars  int
		wantRated  bool
	}{
		{"score-9-clamps-to-5", "9", 5, true},
		{"score-6-clamps-to-5", "6", 5, true},
		{"score-negative-clamps-to-unrated", "-3", 0, false},
		{"score-zero-unrated", "0", 0, false},
		{"score-huge", "1000000", 5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc, st := newTestServer(t)
			entry := appScoredRaw("c", "C", tc.scoreToken, "explanation text")
			fs := feedServer(t, feed(entry))
			defer fs.Close()
			if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
				t.Fatalf("sync: %v", err)
			}
			row := detailRow(t, srv, "official", "c")
			if row == "" {
				t.Fatalf("app absent for token %q", tc.scoreToken)
			}
			filled := strings.Count(row, "\u2605")
			if tc.wantRated {
				if filled != tc.wantStars {
					t.Errorf("token %q: got %d stars, want %d", tc.scoreToken, filled, tc.wantStars)
				}
				if !strings.Contains(row, "rating-explanation") {
					t.Errorf("token %q: rated app should show explanation", tc.scoreToken)
				}
			} else {
				if !strings.Contains(row, "Unrated") {
					t.Errorf("token %q: expected Unrated, got %q", tc.scoreToken, row)
				}
				if strings.Contains(row, "rating-explanation") {
					t.Errorf("token %q: unrated app should not show explanation", tc.scoreToken)
				}
			}
		})
	}
}

// TestE2ENonIntegerScoreHandled verifies that a non-integer score token
// makes the feed unusable for that app (Go int decode fails); depending on
// decoder behavior the whole feed is rejected. Either way, the bad value must
// never render as a score.
func TestE2ENonIntegerScoreHandled(t *testing.T) {
	srv, svc, st := newTestServer(t)
	// 3.5 is a valid JSON number but not a Go int; encoding/json will fail to
	// decode the struct, so the sync should error and the app be absent.
	entry := appScoredRaw("frac", "Frac", "3.5", "ex")
	fs := feedServer(t, feed(entry))
	defer fs.Close()
	err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json")
	// Whether or not it errors, the app must not appear with a bogus score.
	code, _ := get(t, srv, "/apps/official/frac")
	if err == nil && code == 200 {
		row := detailRow(t, srv, "official", "frac")
		if strings.Contains(row, "3/5") || strings.Contains(row, "4/5") {
			t.Errorf("fractional score should not render as an integer rating: %q", row)
		}
	}
}

// TestE2EExplanationXSSEscaped ensures a malicious explanation is HTML-escaped
// on the detail page and never injected as live markup.
func TestE2EExplanationXSSEscaped(t *testing.T) {
	srv, svc, st := newTestServer(t)
	payloads := []string{
		`<script>alert(1)</script>`,
		`"><img src=x onerror=alert(1)>`,
		`</td></tr><tr><td>injected`,
		`{{.Secret}}`,
		`javascript:alert(1)`,
	}
	entries := make([]string, len(payloads))
	for i, p := range payloads {
		entries[i] = appScored(fmt.Sprintf("xss%d", i), fmt.Sprintf("XSS %d", i), 3, p)
	}
	fs := feedServer(t, feed(strings.Join(entries, ",")))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	for i, p := range payloads {
		_, body := get(t, srv, fmt.Sprintf("/apps/official/xss%d", i))
		if strings.Contains(body, "<script>alert(1)</script>") {
			t.Errorf("payload %d: <script> not escaped", i)
		}
		if strings.Contains(body, "<img src=x onerror=alert(1)>") {
			t.Errorf("payload %d: <img> not escaped", i)
		}
		if p == `{{.Secret}}` && !strings.Contains(body, "{{.Secret}}") {
			t.Errorf("payload %d: template braces should render literally", i)
		}
	}
}

// TestE2EExplanationUnicodeAndClamp verifies that long, multi-byte explanations
// are clamped to the ingest limit on a rune boundary and still render as valid
// UTF-8 in the response.
func TestE2EExplanationUnicodeAndClamp(t *testing.T) {
	srv, svc, st := newTestServer(t)
	long := strings.Repeat("é", 500)
	fs := feedServer(t, feed(appScored("uni", "Uni", 4, long)))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	got, err := st.GetCatalogApp(context.Background(), "official", "uni")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if n := utf8.RuneCountInString(got.OpenhostIntegrationScoreExplanation); n != 400 {
		t.Errorf("stored explanation rune count = %d, want 400", n)
	}
	if !utf8.ValidString(got.OpenhostIntegrationScoreExplanation) {
		t.Error("stored explanation is not valid UTF-8")
	}

	code, body := get(t, srv, "/apps/official/uni")
	if code != 200 {
		t.Fatalf("detail code %d", code)
	}
	if !utf8.ValidString(body) {
		t.Error("rendered page is not valid UTF-8")
	}
	if !strings.Contains(body, "rating-explanation") {
		t.Error("clamped explanation should still render")
	}
}

// TestE2EExplanationEmojiClamp exercises 4-byte runes at the clamp boundary.
func TestE2EExplanationEmojiClamp(t *testing.T) {
	srv, svc, st := newTestServer(t)
	long := strings.Repeat("😀", 500) // 4 bytes each
	fs := feedServer(t, feed(appScored("emoji", "Emoji", 5, long)))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got, err := st.GetCatalogApp(context.Background(), "official", "emoji")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if n := utf8.RuneCountInString(got.OpenhostIntegrationScoreExplanation); n != 400 {
		t.Errorf("emoji explanation rune count = %d, want 400", n)
	}
	if !utf8.ValidString(got.OpenhostIntegrationScoreExplanation) {
		t.Error("emoji explanation not valid UTF-8 after clamp")
	}
	_ = srv
}

// TestE2EExplanationWhitespaceTrimmed verifies surrounding whitespace is
// trimmed at ingest, and a whitespace-only explanation becomes empty.
func TestE2EExplanationWhitespaceTrimmed(t *testing.T) {
	srv, svc, st := newTestServer(t)
	entries := strings.Join([]string{
		appScored("ws", "WS", 5, "   spaced out   "),
		appScored("blank", "Blank", 5, "    "),
	}, ",")
	fs := feedServer(t, feed(entries))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got, err := st.GetCatalogApp(context.Background(), "official", "ws")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OpenhostIntegrationScoreExplanation != "spaced out" {
		t.Errorf("explanation not trimmed: %q", got.OpenhostIntegrationScoreExplanation)
	}
	blank, err := st.GetCatalogApp(context.Background(), "official", "blank")
	if err != nil {
		t.Fatalf("get blank: %v", err)
	}
	if blank.OpenhostIntegrationScoreExplanation != "" {
		t.Errorf("whitespace-only explanation should trim to empty, got %q", blank.OpenhostIntegrationScoreExplanation)
	}
	row := detailRow(t, srv, "official", "blank")
	if strings.Contains(row, "rating-explanation") {
		t.Error("blank explanation should not render a block")
	}
}

// TestE2EResyncUpdatesScoreAndExplanation confirms that a source re-sync with
// changed score/explanation replaces the stored values (no stale data).
func TestE2EResyncUpdatesScoreAndExplanation(t *testing.T) {
	srv, svc, st := newTestServer(t)

	fs := newMutableFeedServer(t, feed(appScored("ch", "Changer", 2, "first explanation")))
	if err := addAndSync(t, svc, st, "official", "Off", fs.srv.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync1: %v", err)
	}
	row := detailRow(t, srv, "official", "ch")
	if !strings.Contains(row, "2/5") || !strings.Contains(row, "first explanation") {
		t.Fatalf("after sync1, unexpected row: %q", row)
	}

	fs.set(feed(appScored("ch", "Changer", 5, "second explanation")))
	if err := svc.SyncSource(context.Background(), "official"); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	row = detailRow(t, srv, "official", "ch")
	if !strings.Contains(row, "5/5") {
		t.Errorf("after sync2, expected 5/5: %q", row)
	}
	if !strings.Contains(row, "second explanation") {
		t.Errorf("after sync2, expected updated explanation: %q", row)
	}
	if strings.Contains(row, "first explanation") {
		t.Error("stale explanation survived re-sync")
	}

	fs.set(feed(app("ch", "Changer")))
	if err := svc.SyncSource(context.Background(), "official"); err != nil {
		t.Fatalf("sync3: %v", err)
	}
	row = detailRow(t, srv, "official", "ch")
	if !strings.Contains(row, "Unrated") {
		t.Errorf("after sync3, expected Unrated: %q", row)
	}
	if strings.Contains(row, "second explanation") {
		t.Error("explanation survived after score removed")
	}
}

// TestE2EMultiSourceScoreIsolation verifies two sources can carry the same app
// name with different scores/explanations without cross-contamination.
func TestE2EMultiSourceScoreIsolation(t *testing.T) {
	srv, svc, st := newTestServer(t)

	fsA := feedServer(t, feed(appScored("dup", "Dup A", 5, "source A explanation")))
	defer fsA.Close()
	fsB := feedServer(t, feed(appScored("dup", "Dup B", 2, "source B explanation")))
	defer fsB.Close()

	if err := addAndSync(t, svc, st, "srca", "Source A", fsA.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync A: %v", err)
	}
	if err := addAndSync(t, svc, st, "srcb", "Source B", fsB.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync B: %v", err)
	}

	rowA := detailRow(t, srv, "srca", "dup")
	if !strings.Contains(rowA, "5/5") || !strings.Contains(rowA, "source A explanation") {
		t.Errorf("source A row wrong: %q", rowA)
	}
	rowB := detailRow(t, srv, "srcb", "dup")
	if !strings.Contains(rowB, "2/5") || !strings.Contains(rowB, "source B explanation") {
		t.Errorf("source B row wrong: %q", rowB)
	}
	if strings.Contains(rowA, "source B explanation") || strings.Contains(rowB, "source A explanation") {
		t.Error("explanations leaked across sources")
	}
}

// TestE2EListingOrderByScore confirms the store orders higher scores first with
// unrated last; explanation does not affect ordering.
func TestE2EListingOrderByScore(t *testing.T) {
	_, svc, st := newTestServer(t)
	entries := []string{
		appScored("low", "Low", 1, "low ex"),
		appScored("high", "High", 5, "high ex"),
		app("none", "None"),
		appScored("mid", "Mid", 3, "mid ex"),
	}
	fs := feedServer(t, feed(strings.Join(entries, ",")))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	apps, err := st.ListCatalogApps(context.Background(), store.AppListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"high", "mid", "low", "none"}
	if len(apps) != len(want) {
		t.Fatalf("got %d apps, want %d", len(apps), len(want))
	}
	for i, id := range want {
		if apps[i].AppID != id {
			ids := make([]string, len(apps))
			for j, a := range apps {
				ids[j] = a.AppID
			}
			t.Fatalf("order at %d: got %v, want %v", i, ids, want)
		}
	}
	// Confirm explanation survived into the listing rows.
	for _, a := range apps {
		if a.OpenhostIntegrationScore > 0 && a.OpenhostIntegrationScoreExplanation == "" {
			t.Errorf("rated app %s lost its explanation in the listing", a.AppID)
		}
		if a.OpenhostIntegrationScore == 0 && a.OpenhostIntegrationScoreExplanation != "" {
			t.Errorf("unrated app %s has an explanation in the listing", a.AppID)
		}
	}
}

// TestE2EMigrationRestoreThenRepopulate asserts the higher-level invariant that
// a source sync followed by a re-sync keeps rendering the explanation end to
// end. The store-level DROP/ADD migration idempotency (the actual column
// re-add) is covered directly by TestMigrationRestoresExplanationColumn.
func TestE2EMigrationRestoreThenRepopulate(t *testing.T) {
	srv, svc, st := newTestServer(t)

	fs := feedServer(t, feed(appScored("mig", "Mig", 4, "migration explanation")))
	defer fs.Close()
	if err := addAndSync(t, svc, st, "official", "Off", fs.URL+"/catalog.json"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	row := detailRow(t, srv, "official", "mig")
	if !strings.Contains(row, "migration explanation") {
		t.Fatalf("baseline render missing explanation: %q", row)
	}

	// The dedicated store-level migration test (TestMigrationRestoresExplanationColumn)
	// covers the DROP/ADD idempotency directly; here we assert the higher-level
	// invariant that a re-sync after Init keeps rendering the explanation.
	if err := svc.SyncSource(context.Background(), "official"); err != nil {
		t.Fatalf("resync: %v", err)
	}
	row = detailRow(t, srv, "official", "mig")
	if !strings.Contains(row, "migration explanation") {
		t.Errorf("explanation missing after resync: %q", row)
	}
}
