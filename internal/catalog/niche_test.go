package catalog

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestNicheExplanationByteSliceUTF8 probes whether clamping a long
// multi-byte explanation can produce invalid UTF-8 (byte-slice mid-rune).
func TestNicheExplanationByteSliceUTF8(t *testing.T) {
	const repo = "https://example.invalid/app"
	// Each "🚀" is 4 bytes. 200 of them = 800 bytes, well over 400.
	// 400 is divisible by 4, so 🚀 (4 bytes) would land cleanly; use a
	// 3-byte rune (e.g. "中", U+4E2D) so the 400-byte boundary lands
	// mid-rune (400 % 3 == 1), forcing a mid-rune cut if byte-sliced.
	// 500 three-byte runes = 1500 bytes, 500 runes (over the 400-rune cap).
	long := strings.Repeat("中", 500)
	got, ok := normalizeFeedApp("s", sourceFeedApp{
		Name:                                "u",
		Title:                               "U",
		RepoURL:                             repo,
		OpenhostIntegrationScore:            3,
		OpenhostIntegrationScoreExplanation: long,
	})
	if !ok {
		t.Fatal("expected normalize ok")
	}
	exp := got.OpenhostIntegrationScoreExplanation
	if !utf8.ValidString(exp) {
		t.Errorf("clamped explanation is not valid UTF-8: %q (len=%d bytes)", exp, len(exp))
	}
	if rc := utf8.RuneCountInString(exp); rc > maxScoreExplanationLen {
		t.Errorf("clamped explanation exceeds rune cap: %d runes", rc)
	}
	if rc := utf8.RuneCountInString(exp); rc != maxScoreExplanationLen {
		t.Errorf("expected exactly %d runes, got %d", maxScoreExplanationLen, rc)
	}
}

// TestNicheExplanationEmojiBoundary uses 4-byte emoji where the cut lands
// off a clean multiple too.
func TestNicheExplanationEmojiBoundary(t *testing.T) {
	const repo = "https://example.invalid/app"
	// "🚀a" repeated: 🚀=4 bytes, a=1 byte => 5-byte unit. 400 % 5 = 0,
	// shift by injecting a leading 1-byte char so the 400 boundary lands
	// inside an emoji.
	long := "x" + strings.Repeat("🚀", 500) // 1 + 2000 bytes, 501 runes
	got, _ := normalizeFeedApp("s", sourceFeedApp{
		Name: "e", Title: "E", RepoURL: repo,
		OpenhostIntegrationScore:            5,
		OpenhostIntegrationScoreExplanation: long,
	})
	exp := got.OpenhostIntegrationScoreExplanation
	if !utf8.ValidString(exp) {
		t.Errorf("emoji-clamped explanation invalid UTF-8 (len=%d bytes)", len(exp))
	}
	if rc := utf8.RuneCountInString(exp); rc != maxScoreExplanationLen {
		t.Errorf("expected %d runes, got %d", maxScoreExplanationLen, rc)
	}
}
