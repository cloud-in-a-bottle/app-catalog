package catalog

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestFeedValidationParity guards against drift between this catalog's
// validation and the openhost-apps generate.py that produces the feed. It
// checks the app-name regex and the category set — the two rules that must
// match exactly across the two repos, since a mismatch lets a submission pass
// one side and fail the other.
//
// Set OPENHOST_APPS_GENERATE_PY to the path of openhost-apps/generate.py to
// run it; skipped otherwise so local and offline runs stay green.
func TestFeedValidationParity(t *testing.T) {
	path := os.Getenv("OPENHOST_APPS_GENERATE_PY")
	if path == "" {
		t.Skip("OPENHOST_APPS_GENERATE_PY not set; skipping cross-repo parity check")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generate.py: %v", err)
	}
	text := string(src)

	if want := pyNamePattern(t, text); validIDPattern.String() != want {
		t.Errorf("app-name regex drift:\n  catalog: %s\n  apps:    %s", validIDPattern.String(), want)
	}

	got := SortedCategories()
	want := pyCategories(t, text)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("category set drift:\n  catalog: %v\n  apps:    %v", got, want)
	}
}

func pyNamePattern(t *testing.T, text string) string {
	m := regexp.MustCompile(`_NAME_PATTERN\s*=\s*re\.compile\(\s*r["']([^"']+)["']`).FindStringSubmatch(text)
	if m == nil {
		t.Fatal("could not find _NAME_PATTERN in generate.py")
	}
	return m[1]
}

func pyCategories(t *testing.T, text string) []string {
	block := regexp.MustCompile(`VALID_CATEGORIES\s*=\s*\{([^}]*)\}`).FindStringSubmatch(text)
	if block == nil {
		t.Fatal("could not find VALID_CATEGORIES in generate.py")
	}
	var cats []string
	for _, m := range regexp.MustCompile(`"([a-z0-9-]+)"`).FindAllStringSubmatch(block[1], -1) {
		cats = append(cats, m[1])
	}
	sort.Strings(cats)
	return cats
}
