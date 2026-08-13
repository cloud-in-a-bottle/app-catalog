// Package orgrename moves persisted GitHub owner references off the old
// organization name onto the new one.
//
// The catalog stores each feed's URL in its own database. seedDefaultSource
// only seeds when no source exists yet, so the URL an instance was first
// installed with persists forever -- changing DEFAULT_SOURCE_URL in a later
// release does nothing for instances that already have a source row.
//
// After the GitHub org is renamed those stored URLs keep working, because
// GitHub redirects a renamed owner. GitHub also releases the old organization
// name for anyone to claim, and per GitHub's documentation the new holder can
// create repositories that override the redirect entries. A stale source URL
// would then let whoever claimed the old name serve this instance its app
// catalog, including the repo_url values the installer acts on.
//
// This reconcile does not fix that on its own. Updates are owner-initiated, so
// an instance can sit on pre-rename code indefinitely and never run this code.
// The redirect is what protects those instances, which means the old org name
// must stay in our hands -- either by transferring repositories to a new org and
// keeping the old one (Docker kept dotcloud, Elastic kept elasticsearch; both
// still resolve), or by re-registering the freed name immediately after a
// rename. Holding the name does not break redirects; only creating a colliding
// repository does. This reconcile is hygiene for the instances that do update.
//
// The owner name and the decision to act on it are separate constants: NewOrg
// is settled data, OrgRenameComplete is the switch. While the switch is false
// the rewrite is a total no-op. Rewriting before the org is actually renamed
// would point instances at an owner that does not resolve, which is strictly
// worse than the redirect dependency it removes. Flip OrgRenameComplete only in
// the release that ships with (or after) the rename.
package orgrename

import (
	"net/url"
	"strings"
)

// OldOrg is the owner every currently-deployed instance has persisted.
const OldOrg = "imbue-openhost"

// repoMoves are repository renames that have ALREADY happened. Rewriting to
// them is safe immediately, unlike the org move below: the new path resolves
// now, and a repo rename inside an org can only have its redirect overridden by
// a member of that org, not by an outside claimant.
var repoMoves = map[string]string{
	"openhost-apps": "app-manifest",
}

// NewOrg is the owner to move to. Decided; the GitHub org has not been renamed
// yet.
const NewOrg = "cloud-in-a-bottle"

// OrgRenameComplete gates the rewrite. False until the org has actually been
// renamed; see the package comment.
const OrgRenameComplete = false

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

// RewriteRepo moves an already-renamed repository segment. Always active; see
// repoMoves.
func RewriteRepo(raw string) (string, bool) {
	if raw == "" {
		return raw, false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return raw, false
	}
	if !rewritableHosts[strings.ToLower(u.Hostname())] {
		return raw, false
	}
	segments := strings.Split(u.Path, "/")
	// Path is "/owner/repo/..." so segments == ["", owner, repo, ...].
	if len(segments) < 3 || segments[2] == "" {
		return raw, false
	}
	moved, ok := repoMoves[segments[2]]
	if !ok {
		return raw, false
	}
	segments[2] = moved
	u.Path = strings.Join(segments, "/")
	return u.String(), true
}

// Rewrite applies both moves: the repository renames that have already
// happened (always), and the owner move (only once the rename is marked
// complete).
func Rewrite(raw string) (string, bool) {
	out, changed := RewriteRepo(raw)
	if Enabled() {
		if owned, ok := RewriteOwner(out, OldOrg, NewOrg); ok {
			out, changed = owned, true
		}
	}
	return out, changed
}

// Enabled reports whether persisted owners should be rewritten.
func Enabled() bool { return OrgRenameComplete && NewOrg != "" && NewOrg != OldOrg }
