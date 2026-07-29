package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/imbue-openhost/openhost-catalog/internal/catalog"
)

type submitForm struct {
	Name        string
	Title       string
	Description string
	RepoURL     string
	RepoRef     string
	IconURL     string
	WebsiteURL  string
	DocsURL     string
	Tags        string
	Categories  []string
}

type submitCategory struct {
	Value   string
	Label   string
	Checked bool
}

type submitPageData struct {
	BasePath      string
	RouterBaseURL string
	RepoURL       string
	RepoLabel     string
	ForkURL       string
	Form          submitForm
	Errors        []string
	FilePath      string
	TOMLPreview   string
	AllCategories []submitCategory
}

type submitResult struct {
	FilePath    string
	TOMLPreview string
}

func (s *Server) handleSubmitPage(w http.ResponseWriter, r *http.Request) {
	s.renderSubmit(w, r, http.StatusOK, submitForm{}, nil, submitResult{})
}

func (s *Server) handleSubmitCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderSubmit(w, r, http.StatusBadRequest, submitForm{}, []string{"Invalid form submission."}, submitResult{})
		return
	}

	form := submitForm{
		Name:        strings.TrimSpace(r.Form.Get("name")),
		Title:       strings.TrimSpace(r.Form.Get("title")),
		Description: strings.TrimSpace(r.Form.Get("description")),
		RepoURL:     strings.TrimSpace(r.Form.Get("repo_url")),
		RepoRef:     strings.TrimSpace(r.Form.Get("repo_ref")),
		IconURL:     strings.TrimSpace(r.Form.Get("icon_url")),
		WebsiteURL:  strings.TrimSpace(r.Form.Get("website_url")),
		DocsURL:     strings.TrimSpace(r.Form.Get("docs_url")),
		Tags:        strings.TrimSpace(r.Form.Get("tags")),
		Categories:  r.Form["categories"],
	}

	entry, errs := buildListingEntry(form)
	if len(errs) > 0 {
		s.renderSubmit(w, r, http.StatusBadRequest, form, errs, submitResult{})
		return
	}

	if msg := s.checkRepoPublic(r.Context(), form.RepoURL); msg != "" {
		s.renderSubmit(w, r, http.StatusBadRequest, form, []string{msg}, submitResult{})
		return
	}

	res := submitResult{
		FilePath:    "apps/" + entry.name + "/app.toml",
		TOMLPreview: entry.toml,
	}
	s.renderSubmit(w, r, http.StatusOK, form, nil, res)
}

func (s *Server) renderSubmit(w http.ResponseWriter, r *http.Request, status int, form submitForm, errs []string, res submitResult) {
	s.render(w, status, "submit.html", submitPageData{
		BasePath:      s.basePathForRequest(r),
		RouterBaseURL: s.routerBaseURL(r),
		RepoURL:       s.cfg.SubmitRepoURL,
		RepoLabel:     repoLabel(s.cfg.SubmitRepoURL),
		ForkURL:       buildForkURL(s.cfg.SubmitRepoURL),
		Form:          form,
		Errors:        errs,
		FilePath:      res.FilePath,
		TOMLPreview:   res.TOMLPreview,
		AllCategories: submitCategories(form.Categories),
	})
}

type listingEntry struct {
	name string
	toml string
}

// buildListingEntry validates a submission against the same rules the feed
// ingest enforces and, when valid, renders the app.toml for the new entry.
func buildListingEntry(form submitForm) (listingEntry, []string) {
	var errs []string

	name := strings.TrimSpace(form.Name)
	switch {
	case name == "":
		errs = append(errs, "App name is required.")
	case !catalog.ValidAppID(name):
		errs = append(errs, "App name must be lowercase alphanumeric with optional interior hyphens (e.g. my-app).")
	}

	title := strings.TrimSpace(form.Title)
	if title == "" {
		errs = append(errs, "Title is required.")
	}
	description := strings.TrimSpace(form.Description)
	if description == "" {
		errs = append(errs, "Description is required.")
	}

	repoURL := strings.TrimSpace(form.RepoURL)
	switch {
	case repoURL == "":
		errs = append(errs, "Repository URL is required.")
	case catalog.SafeURL(repoURL) == "":
		errs = append(errs, "Repository URL must be an absolute http(s) URL.")
	default:
		if _, _, ok := parseGitHubRepo(repoURL); !ok {
			errs = append(errs, "Repository URL must be a GitHub repo (https://github.com/owner/repo).")
		}
	}

	iconURL := validateOptionalURL("Icon URL", form.IconURL, &errs)
	websiteURL := validateOptionalURL("Website URL", form.WebsiteURL, &errs)
	docsURL := validateOptionalURL("Docs URL", form.DocsURL, &errs)

	repoRef := strings.TrimSpace(form.RepoRef)
	if strings.ContainsAny(repoRef, " \t\r\n") {
		errs = append(errs, "Repo ref must not contain whitespace.")
	}

	tags := splitCommaList(form.Tags)

	categories := make([]string, 0, len(form.Categories))
	var badCategories []string
	for _, c := range form.Categories {
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
	return listingEntry{name: name, toml: toml}, nil
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

func splitCommaList(s string) []string {
	out := make([]string, 0)
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
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
// fields that are empty. Field order matches the openhost-apps convention.
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
			b.WriteRune(r)
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

// parseGitHubRepo extracts the owner and repo from a github.com repo URL.
func parseGitHubRepo(raw string) (owner, repo string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Host != "github.com" && u.Host != "www.github.com") {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), true
}

// checkRepoPublic verifies the GitHub repo exists and is reachable without
// auth. Returns a user-facing message on failure, or "" when public.
func (s *Server) checkRepoPublic(ctx context.Context, repoURL string) string {
	if _, _, ok := parseGitHubRepo(repoURL); !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, repoURL, nil)
	if err != nil {
		return ""
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "Could not reach GitHub to verify the repository. Try again."
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return ""
	case http.StatusNotFound:
		return "GitHub repository not found. Check the URL and make sure the repo is public."
	default:
		return "Could not verify the repository on GitHub. Try again."
	}
}

func repoLabel(repoURL string) string {
	u, err := url.Parse(repoURL)
	if err != nil || u.Host == "" {
		return repoURL
	}
	return strings.Trim(u.Path, "/")
}

func submitCategories(selected []string) []submitCategory {
	sel := make(map[string]bool, len(selected))
	for _, c := range selected {
		sel[strings.TrimSpace(c)] = true
	}
	cats := catalog.SortedCategories()
	out := make([]submitCategory, 0, len(cats))
	for _, c := range cats {
		out = append(out, submitCategory{Value: c, Label: categoryLabel(c), Checked: sel[c]})
	}
	return out
}
