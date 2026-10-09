package forgejo

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

const (
	trackerType   = "forgejo"
	apiPrefix     = "/api/v1"
	statusOpen    = "open"
	statusClosed  = "closed"
	issuesPerPage = 50
	// maxPages bounds the ListIssues walk so a server that ignores `page`
	// (a misrouted URL, a broken proxy) cannot hang the CLI.
	maxPages = 100
)

// repository is the subset of the Forgejo Repository object we consume.
type repository struct {
	//nolint:tagliatelle // Forgejo wire format
	FullName string `json:"full_name"`
}

// pullRequestRef marks an issue as a pull request. Its contents are ignored;
// only presence matters.
type pullRequestRef struct{}

// issue is the subset of the Forgejo Issue object we consume. PullRequest is
// non-nil when the item is actually a pull request.
type issue struct {
	Number     int         `json:"number"`
	Title      string      `json:"title"`
	Body       string      `json:"body"`
	State      string      `json:"state"`
	Repository *repository `json:"repository"`
	//nolint:tagliatelle // Forgejo wire format
	PullRequest *pullRequestRef `json:"pull_request"`
	//nolint:tagliatelle // Forgejo wire format
	CreatedAt time.Time `json:"created_at"`
}

type forgejoAdapter struct {
	http        *http.Client
	cfg         config.IssueTrackerConfig
	apiBase     string        // "<instance root>/api/v1", no trailing slash
	trackerType string        // cfg.Type as configured ("forgejo" or "gitea")
	proxyAuth   *url.Userinfo // credentials from the URL's userinfo; nil when absent
}

// New creates a Forgejo adapter from cfg. URL is the instance root (e.g.
// https://codeberg.org); a trailing /api/v1 is tolerated.
//
// A URL carrying userinfo (https://user:pass@host) means the instance sits
// behind an HTTP Basic auth gate (a reverse proxy). Those credentials go in
// the Authorization header for the gate, and the Forgejo token is sent as the
// `token` query parameter instead — the only other place Forgejo reads it.
func New(cfg config.IssueTrackerConfig) (tracker.Tracker, error) {
	if cfg.URL == "" {
		return nil, errors.New("forgejo: URL is required")
	}

	if cfg.Token == "" {
		return nil, errors.New("forgejo: token is required")
	}

	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("forgejo: invalid URL: %w", err)
	}

	proxyAuth := u.User
	u.User = nil

	base := strings.TrimRight(u.String(), "/")
	base = strings.TrimSuffix(base, apiPrefix)

	return &forgejoAdapter{
		http:        &http.Client{},
		cfg:         cfg,
		apiBase:     base + apiPrefix,
		trackerType: cmp.Or(cfg.Type, trackerType),
		proxyAuth:   proxyAuth,
	}, nil
}

// doJSON issues one authenticated request against the API base. path is
// relative to apiBase and may carry a query string. A non-nil body is
// JSON-encoded; a non-nil dst receives the decoded response. Non-2xx
// responses are returned as *httpError so callers can branch on the status.
//
// Error messages quote the caller's path only, never the final URL, so the
// token added for the basic-auth-gate case cannot leak into warnings or logs.
func (a *forgejoAdapter) doJSON(ctx context.Context, method, path string, body, dst any) error {
	var payload io.Reader = http.NoBody

	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}

		payload = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.requestURL(path), payload)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if a.proxyAuth != nil {
		pw, _ := a.proxyAuth.Password()
		req.SetBasicAuth(a.proxyAuth.Username(), pw)
	} else {
		req.Header.Set("Authorization", "token "+a.cfg.Token)
	}

	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}

	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &httpError{Method: method, Path: path, StatusCode: resp.StatusCode}
	}

	if dst == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}

	return nil
}

// requestURL joins path onto apiBase. Behind a basic-auth gate the token
// travels as the `token` query parameter (see New).
func (a *forgejoAdapter) requestURL(path string) string {
	full := a.apiBase + path
	if a.proxyAuth == nil {
		return full
	}

	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}

	return full + sep + "token=" + url.QueryEscape(a.cfg.Token)
}

// httpError reports a non-2xx response.
type httpError struct {
	Method     string
	Path       string
	StatusCode int
}

func (e *httpError) Error() string {
	return fmt.Sprintf("%s %s: unexpected status %d", e.Method, e.Path, e.StatusCode)
}

// ListIssues fetches open issues assigned to the authenticated user across
// every repository the token can see via GET /repos/issues/search. It walks
// pages until an empty one comes back (Forgejo may cap `limit` server-side,
// so a short page is not a reliable end signal) or maxPages is reached, drops
// pull requests, and applies the optional client-side Projects filter.
func (a *forgejoAdapter) ListIssues(ctx context.Context) ([]tracker.Issue, error) {
	issues, err := a.listPages(ctx, "/repos/issues/search", url.Values{"assigned": {"true"}})
	if err != nil {
		return nil, fmt.Errorf("forgejo: list issues: %w", err)
	}

	var out []tracker.Issue

	for i := range issues {
		proj := ""
		if issues[i].Repository != nil {
			proj = issues[i].Repository.FullName
		}

		if len(a.cfg.Projects) > 0 && !slices.Contains(a.cfg.FarSlugs(), proj) {
			continue
		}

		out = append(out, a.toIssue(&issues[i], proj))
	}

	return out, nil
}

// listPages walks the open issues of path, page after page, until an empty
// page comes back (Forgejo may cap `limit` server-side, so a short page is not
// a reliable end signal) or maxPages is reached, and drops pull requests. q
// adds to the query.
func (a *forgejoAdapter) listPages(ctx context.Context, path string, q url.Values) ([]issue, error) {
	var out []issue

	q.Set("state", statusOpen)
	q.Set("type", "issues")
	q.Set("limit", strconv.Itoa(issuesPerPage))

	for page := 1; page <= maxPages; page++ {
		q.Set("page", strconv.Itoa(page))

		var batch []issue
		if err := a.doJSON(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &batch); err != nil {
			return nil, err
		}

		if len(batch) == 0 {
			break
		}

		for i := range batch {
			if batch[i].PullRequest == nil {
				out = append(out, batch[i])
			}
		}
	}

	return out, nil
}

// toIssue converts a Forgejo issue of project (an "owner/repo").
func (a *forgejoAdapter) toIssue(iss *issue, project string) tracker.Issue {
	return tracker.Issue{
		TrackerType: a.trackerType,
		ID:          strconv.Itoa(iss.Number),
		Subject:     iss.Title,
		Description: iss.Body,
		Status:      iss.State,
		Project:     project,
		CreatedAt:   iss.CreatedAt,
	}.Clean()
}

// projectIssuesPath returns "/repos/{owner}/{repo}/issues" and the project name.
func (a *forgejoAdapter) projectIssuesPath() (path, project string, err error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return "", "", err
	}

	return fmt.Sprintf("/repos/%s/%s/issues", url.PathEscape(owner), url.PathEscape(repo)), owner + "/" + repo, nil
}

// ListProjectIssues fetches every open issue of the configured repository,
// whoever it is assigned to, the way ListIssues walks pages.
func (a *forgejoAdapter) ListProjectIssues(ctx context.Context) ([]tracker.Issue, error) {
	path, project, err := a.projectIssuesPath()
	if err != nil {
		return nil, err
	}

	issues, err := a.listPages(ctx, path, url.Values{})
	if err != nil {
		return nil, fmt.Errorf("forgejo: list issues of %s: %w", project, err)
	}

	out := make([]tracker.Issue, len(issues))
	for i := range issues {
		out[i] = a.toIssue(&issues[i], project)
	}

	return out, nil
}

// CreateIssue creates an issue in the configured repository.
func (a *forgejoAdapter) CreateIssue(ctx context.Context, title, description string) (tracker.Issue, error) {
	path, project, err := a.projectIssuesPath()
	if err != nil {
		return tracker.Issue{}, err
	}

	body := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Title: title, Body: description}

	var created issue
	if err := a.doJSON(ctx, http.MethodPost, path, body, &created); err != nil {
		return tracker.Issue{}, fmt.Errorf("forgejo: create issue: %w", err)
	}

	return a.toIssue(&created, project), nil
}

// SetIssueOpen reopens (open) or closes the issue.
func (a *forgejoAdapter) SetIssueOpen(ctx context.Context, issueID string, open bool) error {
	state := statusClosed
	if open {
		state = statusOpen
	}

	return a.UpdateIssueStatus(ctx, issueID, state)
}

// ListStatuses returns the static set of Forgejo issue states (open, closed).
func (*forgejoAdapter) ListStatuses(_ context.Context) ([]string, error) {
	return []string{statusOpen, statusClosed}, nil
}

// ownerRepo resolves the single "owner/repo" entry from cfg.Projects.
// It returns an error when Projects does not contain exactly one valid entry.
func (a *forgejoAdapter) ownerRepo() (owner, repo string, err error) {
	if len(a.cfg.Projects) != 1 {
		return "", "", fmt.Errorf("forgejo: exactly one project must be configured (got %d)", len(a.cfg.Projects))
	}

	owner, repo, ok := strings.Cut(a.cfg.Projects[0].FarSlug, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", fmt.Errorf("forgejo: invalid project %q (expected owner/repo)", a.cfg.Projects[0].FarSlug)
	}

	return owner, repo, nil
}

// issuePath builds "/repos/{owner}/{repo}/issues/{n}" for issueID, validating
// both the configured project and the numeric id.
func (a *forgejoAdapter) issuePath(issueID string) (string, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return "", err
	}

	n, err := strconv.Atoi(issueID)
	if err != nil {
		return "", fmt.Errorf("forgejo: invalid issue id %q: %w", issueID, err)
	}

	return fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(owner), url.PathEscape(repo), n), nil
}

// UpdateIssueStatus toggles the issue's state to "open" or "closed" via
// PATCH /repos/{owner}/{repo}/issues/{number}. The owner/repo is taken from
// cfg.Projects, which must contain exactly one "owner/repo" entry.
func (a *forgejoAdapter) UpdateIssueStatus(ctx context.Context, issueID, statusName string) error {
	state, err := mapState(statusName)
	if err != nil {
		return err
	}

	path, err := a.issuePath(issueID)
	if err != nil {
		return err
	}

	body := struct {
		State string `json:"state"`
	}{State: state}

	if err := a.doJSON(ctx, http.MethodPatch, path, body, nil); err != nil {
		return fmt.Errorf("forgejo: edit issue %s: %w", issueID, err)
	}

	return nil
}

func mapState(name string) (string, error) {
	switch name {
	case statusOpen, statusClosed:
		return name, nil
	default:
		return "", fmt.Errorf("forgejo: unknown status %q (want %q or %q)", name, statusOpen, statusClosed)
	}
}

// IsIssueClosed asks Forgejo for the issue's state via
// GET /repos/{owner}/{repo}/issues/{number}. HTTP 404 → tracker.ErrIssueNotFound;
// other failures are wrapped.
func (a *forgejoAdapter) IsIssueClosed(ctx context.Context, issueID string) (bool, error) {
	path, err := a.issuePath(issueID)
	if err != nil {
		return false, err
	}

	var iss issue
	if err := a.doJSON(ctx, http.MethodGet, path, nil, &iss); err != nil {
		var he *httpError
		if errors.As(err, &he) {
			if he.StatusCode == http.StatusNotFound {
				return false, tracker.ErrIssueNotFound
			}
		}

		return false, fmt.Errorf("forgejo: get issue %s: %w", issueID, err)
	}

	return iss.State == statusClosed, nil
}

// AddComment posts body as an issue comment via
// POST /repos/{owner}/{repo}/issues/{number}/comments.
func (a *forgejoAdapter) AddComment(ctx context.Context, issueID, body string) error {
	path, err := a.issuePath(issueID)
	if err != nil {
		return err
	}

	payload := struct {
		Body string `json:"body"`
	}{Body: body}

	if err := a.doJSON(ctx, http.MethodPost, path+"/comments", payload, nil); err != nil {
		return fmt.Errorf("forgejo: comment on issue %s: %w", issueID, err)
	}

	return nil
}
