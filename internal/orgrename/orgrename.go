// Package orgrename moves persisted GitHub owner references off the old
// organization name onto the new one.
//
// The catalog stores each feed's URL in its own database. seedDefaultSource
// only seeds when no source exists yet, so the URL an instance was first
// installed with persists forever -- changing DEFAULT_SOURCE_URL in a later
// release does nothing for instances that already have a source row.
//
// After the GitHub org is renamed those stored URLs keep working, because
// GitHub redirects a renamed owner. That redirect is a migration window, not a
// destination: GitHub releases the old organization name for anyone to claim,
// and per GitHub's documentation the new holder can create repositories that
// override the redirect entries. A stale source URL would then let whoever
// claimed the old name serve this instance its app catalog, including the
// repo_url values the installer acts on.
//
// NewOrg ships empty, which disables the rewrite completely. Rewriting before
// the org is actually renamed would point instances at an owner that does not
// exist, which is strictly worse than the redirect dependency it removes. Set
// NewOrg only in the release that ships with (or after) the rename.
package orgrename

import (
	"net/url"
	"strings"
)

// OldOrg is the owner every currently-deployed instance has persisted.
const OldOrg = "imbue-openhost"

// NewOrg is the owner to move to. Empty disables the rewrite. The name itself
// is still pending sign-off.
const NewOrg = ""

// rewritableHosts are the only hosts whose first path segment is a GitHub
// owner. Matched by exact equality so a look-alike such as
// github.com.evil.example is never rewritten.
var rewritableHosts = map[string]bool{
	"github.com":                true,
	"www.github.com":            true,
	"raw.githubusercontent.com": true,
}

// RewriteOwner returns raw with its GitHub owner moved from oldOrg to newOrg,
// and whether it changed anything.
//
// Only the owner segment moves. The repository name, the rest of the path, and
// any query or fragment are preserved, including for a repository whose name
// happens to contain the old owner string.
func RewriteOwner(raw, oldOrg, newOrg string) (string, bool) {
	if newOrg == "" || oldOrg == "" || newOrg == oldOrg || raw == "" {
		return raw, false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw, false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return raw, false
	}
	if !rewritableHosts[strings.ToLower(u.Hostname())] {
		return raw, false
	}
	// Path is "/owner/repo/..." so segments == ["", owner, repo, ...].
	segments := strings.Split(u.Path, "/")
	if len(segments) < 3 || segments[1] == "" {
		return raw, false
	}
	// GitHub owner names are case-insensitive; compare accordingly but write
	// the new owner exactly as configured.
	if !strings.EqualFold(segments[1], oldOrg) {
		return raw, false
	}
	segments[1] = newOrg
	u.Path = strings.Join(segments, "/")
	return u.String(), true
}

// Rewrite applies RewriteOwner using the package defaults.
func Rewrite(raw string) (string, bool) {
	return RewriteOwner(raw, OldOrg, NewOrg)
}

// Enabled reports whether a new owner has been configured.
func Enabled() bool { return NewOrg != "" }
