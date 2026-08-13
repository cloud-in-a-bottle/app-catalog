package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/imbue-openhost/openhost-catalog/internal/catalog"
)

// submitTemplateTOML seeds the editor with a dummy entry. Required fields carry
// placeholder values; optional fields are commented out.
const submitTemplateTOML = `[app]
name = "my-app"
title = "My App"
description = "One-line summary of what the app does."
repo_url = "https://github.com/you/your-app"

# Optional — uncomment and edit any you need:
# repo_ref = "main"
# icon_url = "https://example.com/icon.png"
# website_url = "https://example.com"
# docs_url = "https://example.com/docs"
# tags = ["example", "self-hosted"]
# categories = ["productivity"]
`

type appManifest struct {
	App appManifestApp `toml:"app"`
}

type appManifestApp struct {
	Name        string   `toml:"name"`
	Title       string   `toml:"title"`
	Description string   `toml:"description"`
	RepoURL     string   `toml:"repo_url"`
	RepoRef     string   `toml:"repo_ref"`
	IconURL     string   `toml:"icon_url"`
	WebsiteURL  string   `toml:"website_url"`
	DocsURL     string   `toml:"docs_url"`
	Tags        []string `toml:"tags"`
	Categories  []string `toml:"categories"`
}

// fieldDoc describes one app.toml key for the reference list under the editor.
type fieldDoc struct {
	Key  string
	Desc string
}

var submitFieldDocs = []fieldDoc{
	{"name", "Required. Lowercase, hyphenated; the name the app deploys as (e.g. my-app)."},
	{"title", "Required. Display name."},
	{"description", "Required. One-line summary."},
	{"repo_url", "Required. Public GitHub repo containing the app's openhost.toml."},
	{"repo_ref", "Optional. Branch, tag, or commit to pin."},
	{"icon_url", "Optional. Absolute http(s) URL to an icon."},
	{"website_url", "Optional. Upstream project homepage."},
	{"docs_url", "Optional. Documentation URL."},
	{"tags", `Optional. Array of strings, e.g. ["rss", "news"].`},
	{"categories", "Optional. Array drawn from the allowed categories below."},
}

type submitPageData struct {
	BasePath      string
	RouterBaseURL string
	RepoURL       string
	RepoLabel     string
	ForkURL       string
	TOMLInput     string
	FieldDocs     []fieldDoc
	Categories    []string
	Errors        []string
	FilePath      string
	TOMLPreview   string
}

type submitResult struct {
	FilePath    string
	TOMLPreview string
}

func (s *Server) handleSubmitPage(w http.ResponseWriter, r *http.Request) {
	s.renderSubmit(w, r, http.StatusOK, submitTemplateTOML, nil, submitResult{})
}

func (s *Server) handleSubmitCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderSubmit(w, r, http.StatusBadRequest, submitTemplateTOML, []string{"Invalid form submission."}, submitResult{})
		return
	}

	raw := r.Form.Get("toml")
	entry, errs := buildListingEntry(raw)
	if len(errs) > 0 {
		s.renderSubmit(w, r, http.StatusBadRequest, raw, errs, submitResult{})
		return
	}

	if msg := s.checkRepo(r.Context(), entry.repoURL, entry.repoRef); msg != "" {
		s.renderSubmit(w, r, http.StatusBadRequest, raw, []string{msg}, submitResult{})
		return
	}

	if msg := s.checkLinks(r.Context(), entry); msg != "" {
		s.renderSubmit(w, r, http.StatusBadRequest, raw, []string{msg}, submitResult{})
		return
	}

	res := submitResult{
		FilePath:    "apps/" + entry.name + "/app.toml",
		TOMLPreview: entry.toml,
	}
	s.renderSubmit(w, r, http.StatusOK, raw, nil, res)
}

func (s *Server) renderSubmit(w http.ResponseWriter, r *http.Request, status int, tomlInput string, errs []string, res submitResult) {
	s.render(w, status, "submit.html", submitPageData{
		BasePath:      s.basePathForRequest(r),
		RouterBaseURL: s.routerBaseURL(r),
		RepoURL:       s.cfg.SubmitRepoURL,
		RepoLabel:     repoLabel(s.cfg.SubmitRepoURL),
		ForkURL:       buildForkURL(s.cfg.SubmitRepoURL),
		TOMLInput:     tomlInput,
		FieldDocs:     submitFieldDocs,
		Categories:    catalog.SortedCategories(),
		Errors:        errs,
		FilePath:      res.FilePath,
		TOMLPreview:   res.TOMLPreview,
	})
}

type listingEntry struct {
	name       string
	toml       string
	repoURL    string
	repoRef    string
	iconURL    string
	websiteURL string
	docsURL    string
}

// buildListingEntry parses the submitted app.toml, validates it against the
// same rules the feed ingest enforces, and re-renders it in canonical form.
func buildListingEntry(rawTOML string) (listingEntry, []string) {
	if strings.TrimSpace(rawTOML) == "" {
		return listingEntry{}, []string{"Paste your app.toml entry."}
	}
	var m appManifest
	if _, err := toml.Decode(rawTOML, &m); err != nil {
		return listingEntry{}, []string{"Could not parse TOML: " + err.Error()}
	}
	app := m.App

	var errs []string

	name := strings.TrimSpace(app.Name)
	switch {
	case name == "":
		errs = append(errs, "name is required.")
	case !catalog.ValidAppID(name):
		errs = append(errs, "name must be lowercase alphanumeric with optional interior hyphens (e.g. my-app).")
	}

	title := strings.TrimSpace(app.Title)
	if title == "" {
		errs = append(errs, "title is required.")
	}
	description := strings.TrimSpace(app.Description)
	if description == "" {
		errs = append(errs, "description is required.")
	}

	repoURL := strings.TrimSpace(app.RepoURL)
	switch {
	case repoURL == "":
		errs = append(errs, "repo_url is required.")
	default:
		if _, _, _, ok := parseGitHubRepo(repoURL); !ok {
			errs = append(errs, "repo_url must be a GitHub repo (https://github.com/owner/repo).")
		}
	}

	iconURL := validateOptionalURL("icon_url", app.IconURL, &errs)
	websiteURL := validateOptionalURL("website_url", app.WebsiteURL, &errs)
	docsURL := validateOptionalURL("docs_url", app.DocsURL, &errs)

	repoRef := strings.TrimSpace(app.RepoRef)
	if strings.ContainsAny(repoRef, " \t\r\n") {
		errs = append(errs, "repo_ref must not contain whitespace.")
	}

	tags := compactStrings(app.Tags)

	categories := make([]string, 0, len(app.Categories))
	var badCategories []string
	for _, c := range app.Categories {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !catalog.IsAllowedCategory(c) {
			badCategories = append(badCategories, c)
			continue
		}
		categories = append(categories, c)
	}
	if len(badCategories) > 0 {
		errs = append(errs, "Unknown categories: "+strings.Join(badCategories, ", ")+".")
	}

	if len(errs) > 0 {
		return listingEntry{}, errs
	}

	toml := buildAppTOML(appTOMLFields{
		Name:        name,
		Title:       title,
		Description: description,
		RepoURL:     repoURL,
		RepoRef:     repoRef,
		IconURL:     iconURL,
		WebsiteURL:  websiteURL,
		DocsURL:     docsURL,
		Tags:        tags,
		Categories:  categories,
	})
	return listingEntry{
		name:       name,
		toml:       toml,
		repoURL:    repoURL,
		repoRef:    repoRef,
		iconURL:    iconURL,
		websiteURL: websiteURL,
		docsURL:    docsURL,
	}, nil
}

func validateOptionalURL(label, raw string, errs *[]string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if catalog.SafeURL(raw) == "" {
		*errs = append(*errs, label+" must be an absolute http(s) URL.")
		return ""
	}
	return raw
}

func compactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

type appTOMLFields struct {
	Name        string
	Title       string
	Description string
	RepoURL     string
	RepoRef     string
	IconURL     string
	WebsiteURL  string
	DocsURL     string
	Tags        []string
	Categories  []string
}

// buildAppTOML renders an apps/<name>/app.toml entry, omitting optional
// fields that are empty. Field order matches the app-manifest convention.
func buildAppTOML(f appTOMLFields) string {
	var b strings.Builder
	b.WriteString("[app]\n")
	b.WriteString("name = " + tomlString(f.Name) + "\n")
	b.WriteString("title = " + tomlString(f.Title) + "\n")
	b.WriteString("description = " + tomlString(f.Description) + "\n")
	b.WriteString("repo_url = " + tomlString(f.RepoURL) + "\n")
	if f.RepoRef != "" {
		b.WriteString("repo_ref = " + tomlString(f.RepoRef) + "\n")
	}
	if f.IconURL != "" {
		b.WriteString("icon_url = " + tomlString(f.IconURL) + "\n")
	}
	if f.WebsiteURL != "" {
		b.WriteString("website_url = " + tomlString(f.WebsiteURL) + "\n")
	}
	if f.DocsURL != "" {
		b.WriteString("docs_url = " + tomlString(f.DocsURL) + "\n")
	}
	if len(f.Tags) > 0 {
		b.WriteString("tags = " + tomlStringArray(f.Tags) + "\n")
	}
	if len(f.Categories) > 0 {
		b.WriteString("categories = " + tomlStringArray(f.Categories) + "\n")
	}
	return b.String()
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func tomlStringArray(items []string) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = tomlString(it)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// buildForkURL points to GitHub's fork dialog for the feed repo.
func buildForkURL(repoURL string) string {
	return strings.TrimRight(repoURL, "/") + "/fork"
}

// parseGitHubRepo extracts the owner, repo, and any /tree|/blob ref from an
// http(s) github.com URL. ref is "" when the URL carries no branch.
func parseGitHubRepo(raw string) (owner, repo, ref string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", "", false
	}
	if u.Host != "github.com" && u.Host != "www.github.com" {
		return "", "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", false
	}
	if len(parts) >= 4 && (parts[2] == "tree" || parts[2] == "blob") {
		ref = parts[3]
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), ref, true
}

// checkRepo verifies, without auth, that the GitHub repo is public and has an
// openhost.toml at its root. Returns a user-facing message on failure, or "".
func (s *Server) checkRepo(ctx context.Context, repoURL, repoRef string) string {
	owner, repo, urlRef, ok := parseGitHubRepo(repoURL)
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()

	if exists, notFound := classifyHead(s.headStatus(ctx, "https://github.com/"+owner+"/"+repo)); notFound {
		return "GitHub repository not found. Check the URL and make sure the repo is public."
	} else if !exists {
		return "Could not reach GitHub to verify the repository. Try again."
	}

	found, reachable := s.findManifest(ctx, owner, repo, candidateRefs(repoRef, urlRef))
	if found {
		return ""
	}
	if !reachable {
		return "Could not reach GitHub to verify openhost.toml. Try again."
	}
	return "No openhost.toml found at the repository root."
}

var lookupHost = net.DefaultResolver.LookupHost

// checkLinks verifies the optional icon, website, and docs URLs point at
// domains that resolve. Returns a user-facing message on the first failure.
func (s *Server) checkLinks(ctx context.Context, entry listingEntry) string {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	for _, l := range []struct{ label, rawURL string }{
		{"icon_url", entry.iconURL},
		{"website_url", entry.websiteURL},
		{"docs_url", entry.docsURL},
	} {
		if l.rawURL != "" && !hostResolves(ctx, l.rawURL) {
			return l.label + " domain does not resolve. Check the URL."
		}
	}
	return ""
}

// hostResolves reports whether rawURL's host resolves via DNS.
func hostResolves(ctx context.Context, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	addrs, err := lookupHost(ctx, u.Hostname())
	return err == nil && len(addrs) > 0
}

// candidateRefs orders the refs to probe for openhost.toml: an explicit pin,
// any ref from the URL, then the default branch. Falling back to the default
// branch means a pasted /tree/<branch> link still validates.
func candidateRefs(repoRef, urlRef string) []string {
	refs := make([]string, 0, 3)
	seen := make(map[string]bool, 3)
	for _, ref := range []string{repoRef, urlRef, "HEAD"} {
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}

// findManifest reports whether openhost.toml exists at the root of any ref.
// reachable is false only when every probe failed to reach GitHub.
func (s *Server) findManifest(ctx context.Context, owner, repo string, refs []string) (found, reachable bool) {
	for _, ref := range refs {
		u := "https://raw.githubusercontent.com/" + owner + "/" + repo + "/" + ref + "/openhost.toml"
		exists, notFound := classifyHead(s.headStatus(ctx, u))
		if exists {
			return true, true
		}
		if notFound {
			reachable = true
		}
	}
	return false, reachable
}

// classifyHead maps a HEAD status to existence: a 2xx or redirect means the
// resource exists, 404 means it does not, anything else is unreachable.
func classifyHead(status int) (exists, notFound bool) {
	switch {
	case status >= 200 && status < 400:
		return true, false
	case status == http.StatusNotFound:
		return false, true
	default:
		return false, false
	}
}

// headStatus issues a HEAD request and returns the status code, or 0 on error.
func (s *Server) headStatus(ctx context.Context, rawURL string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("User-Agent", "openhost-catalog")
	resp, err := s.http.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

func repoLabel(repoURL string) string {
	u, err := url.Parse(repoURL)
	if err != nil || u.Host == "" {
		return repoURL
	}
	return strings.Trim(u.Path, "/")
}
