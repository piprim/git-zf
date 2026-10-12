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

// label is the subset of the Forgejo Label object we consume.
type label struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// issue is the subset of the Forgejo Issue object we consume. PullRequest is
// non-nil when the item is actually a pull request.
type issue struct {
	Number     int         `json:"number"`
	Title      string      `json:"title"`
	Body       string      `json:"body"`
	State      string      `json:"state"`
	Labels     []label     `json:"labels"`
	Repository *repository `json:"repository"`
	//nolint:tagliatelle // Forgejo wire format
	PullRequest *pullRequestRef `json:"pull_request"`
	//nolint:tagliatelle // Forgejo wire format
	CreatedAt time.Time `json:"created_at"`
	//nolint:tagliatelle // Forgejo wire format
	UpdatedAt time.Time `json:"updated_at"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
}

// comment is the subset of the Forgejo Comment object we consume.
type comment struct {
	ID   int    `json:"id"`
	Body string `json:"body"`
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	//nolint:tagliatelle // Forgejo wire format
	CreatedAt time.Time `json:"created_at"`
}

type forgejoAdapter struct {
	http        *http.Client
	cfg         config.IssueTrackerConfig
	apiBase     string        // "<instance root>/api/v1", no trailing slash
	trackerType string        // cfg.Type as configured ("forgejo" or "gitea")
	proxyAuth   *url.Userinfo // credentials from the URL's userinfo; nil when absent
	meLogin     string        // the authenticated user's login once me looked it up
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

// ListIssues fetches every open issue of each configured repository, whoever
// it is assigned to. Without projects, it fetches every open issue the token
// can see across the instance, via GET /repos/issues/search. Pages are walked
// by listPages; pull requests are dropped.
func (a *forgejoAdapter) ListIssues(ctx context.Context) ([]tracker.Issue, error) {
	if len(a.cfg.Projects) == 0 {
		issues, err := a.listPages(ctx, "/repos/issues/search", url.Values{})
		if err != nil {
			return nil, fmt.Errorf("forgejo: list issues: %w", err)
		}

		return a.toIssues(ctx, issues, "")
	}

	var out []tracker.Issue

	for _, id := range a.cfg.IDs() {
		owner, repo, err := splitRepo(id)
		if err != nil {
			return nil, err
		}

		issues, err := a.listPages(ctx, issuesPath(owner, repo), url.Values{})
		if err != nil {
			return nil, fmt.Errorf("forgejo: list issues of %s: %w", id, err)
		}

		converted, err := a.toIssues(ctx, issues, owner+"/"+repo)
		if err != nil {
			return nil, err
		}

		out = append(out, converted...)
	}

	return out, nil
}

// toIssues converts a listing. Every issue is reported under project; when
// project is empty, the name comes from the issue's repository. The
// authenticated user is looked up once, and only when an issue has an
// assignee, to set AssignedToMe.
func (a *forgejoAdapter) toIssues(ctx context.Context, issues []issue, project string) ([]tracker.Issue, error) {
	assigned := false
	for i := range issues {
		assigned = assigned || len(issues[i].Assignees) > 0
	}
	if assigned && a.meLogin == "" {
		var me struct {
			Login string `json:"login"`
		}
		if err := a.doJSON(ctx, http.MethodGet, "/user", nil, &me); err != nil {
			return nil, fmt.Errorf("forgejo: current user: %w", err)
		}
		a.meLogin = me.Login
	}

	out := make([]tracker.Issue, len(issues))
	for i := range issues {
		proj := project
		if proj == "" && issues[i].Repository != nil {
			proj = issues[i].Repository.FullName
		}
		out[i] = a.toIssue(&issues[i], proj)
		for _, u := range issues[i].Assignees {
			out[i].AssignedToMe = out[i].AssignedToMe || u.Login == a.meLogin
		}
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
	labels := make([]string, len(iss.Labels))
	for i, l := range iss.Labels {
		labels[i] = l.Name
	}

	return tracker.Issue{
		TrackerType: a.trackerType,
		ID:          strconv.Itoa(iss.Number),
		Subject:     iss.Title,
		Description: iss.Body,
		Status:      iss.State,
		Project:     project,
		Labels:      labels,
		CreatedAt:   iss.CreatedAt,
		UpdatedAt:   iss.UpdatedAt,
		Closed:      iss.State == statusClosed,
	}.Clean()
}

// projectIssuesPath returns "/repos/{owner}/{repo}/issues" and the project name.
func (a *forgejoAdapter) projectIssuesPath() (path, project string, err error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return "", "", err
	}

	return issuesPath(owner, repo), owner + "/" + repo, nil
}

// issuesPath returns "/repos/{owner}/{repo}/issues".
func issuesPath(owner, repo string) string {
	return fmt.Sprintf("/repos/%s/%s/issues", url.PathEscape(owner), url.PathEscape(repo))
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

	return splitRepo(a.cfg.Projects[0].ID)
}

// splitRepo splits a project ID into its owner and repository.
func splitRepo(id string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(id, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", fmt.Errorf("forgejo: invalid project %q (expected owner/repo)", id)
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

// GetIssue reads one issue via GET /repos/{owner}/{repo}/issues/{number}.
// HTTP 404 → tracker.ErrIssueNotFound; other failures are wrapped.
func (a *forgejoAdapter) GetIssue(ctx context.Context, issueID string) (tracker.Issue, error) {
	path, err := a.issuePath(issueID)
	if err != nil {
		return tracker.Issue{}, err
	}
	_, project, err := a.projectIssuesPath()
	if err != nil {
		return tracker.Issue{}, err
	}

	var iss issue
	if err := a.doJSON(ctx, http.MethodGet, path, nil, &iss); err != nil {
		var he *httpError
		if errors.As(err, &he) && he.StatusCode == http.StatusNotFound {
			return tracker.Issue{}, tracker.ErrIssueNotFound
		}

		return tracker.Issue{}, fmt.Errorf("forgejo: get issue %s: %w", issueID, err)
	}

	return a.toIssue(&iss, project), nil
}

// IsIssueClosed asks Forgejo for the issue's state.
func (a *forgejoAdapter) IsIssueClosed(ctx context.Context, issueID string) (bool, error) {
	iss, err := a.GetIssue(ctx, issueID)
	if err != nil {
		return false, err
	}

	return iss.Closed, nil
}

// AddComment posts body as an issue comment via
// POST /repos/{owner}/{repo}/issues/{number}/comments.
func (a *forgejoAdapter) AddComment(ctx context.Context, issueID, body string) (tracker.Comment, error) {
	path, err := a.issuePath(issueID)
	if err != nil {
		return tracker.Comment{}, err
	}

	payload := struct {
		Body string `json:"body"`
	}{Body: body}

	var created comment
	if err := a.doJSON(ctx, http.MethodPost, path+"/comments", payload, &created); err != nil {
		return tracker.Comment{}, fmt.Errorf("forgejo: comment on issue %s: %w", issueID, err)
	}
	author := ""
	if created.User != nil {
		author = created.User.Login
	}

	return tracker.Comment{ID: strconv.Itoa(created.ID), Author: author, Body: created.Body, CreatedAt: created.CreatedAt}.Clean(), nil
}

// UpdateIssue sets title and body via PATCH /repos/{owner}/{repo}/issues/{number}.
func (a *forgejoAdapter) UpdateIssue(ctx context.Context, issueID, title, description string) error {
	path, err := a.issuePath(issueID)
	if err != nil {
		return err
	}

	body := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Title: title, Body: description}

	if err := a.doJSON(ctx, http.MethodPatch, path, body, nil); err != nil {
		return fmt.Errorf("forgejo: edit issue %s: %w", issueID, err)
	}

	return nil
}

// repoLabels lists the labels an issue of the repository can carry: the
// repository's, then the owning organisation's (none when the owner is a
// user, 404, or when the token lacks the read:organization scope, 403). A
// name defined at both levels is the repository's.
func (a *forgejoAdapter) repoLabels(ctx context.Context) (path string, labels []label, err error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return "", nil, err
	}
	path = fmt.Sprintf("/repos/%s/%s/labels", url.PathEscape(owner), url.PathEscape(repo))
	if labels, err = a.pageLabels(ctx, path); err != nil {
		return "", nil, err
	}
	org, err := a.pageLabels(ctx, "/orgs/"+url.PathEscape(owner)+"/labels")
	var he *httpError
	if errors.As(err, &he) && (he.StatusCode == http.StatusNotFound || he.StatusCode == http.StatusForbidden) {
		return path, labels, nil
	}
	if err != nil {
		return "", nil, err
	}
	names := make(map[string]bool, len(labels))
	for _, l := range labels {
		names[l.Name] = true
	}
	for _, l := range org {
		if !names[l.Name] {
			labels = append(labels, l)
		}
	}

	return path, labels, nil
}

// pageLabels lists the labels under path, every page.
func (a *forgejoAdapter) pageLabels(ctx context.Context, path string) (labels []label, err error) {
	for page := 1; page <= maxPages; page++ {
		q := url.Values{"limit": {strconv.Itoa(issuesPerPage)}, "page": {strconv.Itoa(page)}}

		var batch []label
		if err := a.doJSON(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &batch); err != nil {
			return nil, fmt.Errorf("forgejo: list labels: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		labels = append(labels, batch...)
	}

	return labels, nil
}

// ListLabels returns the names of the repository's and the organisation's labels.
func (a *forgejoAdapter) ListLabels(ctx context.Context) ([]string, error) {
	_, labels, err := a.repoLabels(ctx)
	if err != nil {
		return nil, err
	}

	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}

	return names, nil
}

// defaultLabelColor is the colour of a label git-zf creates.
const defaultLabelColor = "#ededed"

// SetLabels replaces the issue's labels with names, creating the ones
// neither the repository nor the issue has, via
// PUT /repos/{owner}/{repo}/issues/{number}/labels.
func (a *forgejoAdapter) SetLabels(ctx context.Context, issueID string, names []string) error {
	issuePath, err := a.issuePath(issueID)
	if err != nil {
		return err
	}

	labelsPath, labels, err := a.repoLabels(ctx)
	if err != nil {
		return err
	}
	// The issue's own labels come with their IDs: an organisation label the
	// token cannot list (403) is kept, not recreated in the repository.
	var cur issue
	if err := a.doJSON(ctx, http.MethodGet, issuePath, nil, &cur); err != nil {
		return fmt.Errorf("forgejo: get issue %s: %w", issueID, err)
	}
	byName := make(map[string]int, len(labels)+len(cur.Labels))
	for _, l := range cur.Labels {
		byName[l.Name] = l.ID
	}
	for _, l := range labels {
		byName[l.Name] = l.ID
	}

	ids := make([]int, 0, len(names))
	for _, name := range names {
		id, ok := byName[name]
		if !ok {
			var created label
			body := struct {
				Name  string `json:"name"`
				Color string `json:"color"`
			}{Name: name, Color: defaultLabelColor}
			if err := a.doJSON(ctx, http.MethodPost, labelsPath, body, &created); err != nil {
				return fmt.Errorf("forgejo: create label %q: %w", name, err)
			}
			id = created.ID
		}
		ids = append(ids, id)
	}

	body := struct {
		Labels []int `json:"labels"`
	}{Labels: ids}
	if err := a.doJSON(ctx, http.MethodPut, issuePath+"/labels", body, nil); err != nil {
		return fmt.Errorf("forgejo: set labels of issue %s: %w", issueID, err)
	}

	return nil
}

// ListComments returns the issue's comments, oldest first, via
// GET /repos/{owner}/{repo}/issues/{number}/comments. A comment with an empty
// body (an event Forgejo lists as a comment) is dropped.
func (a *forgejoAdapter) ListComments(ctx context.Context, issueID string) ([]tracker.Comment, error) {
	path, err := a.issuePath(issueID)
	if err != nil {
		return nil, err
	}

	var out []tracker.Comment
	for page := 1; page <= maxPages; page++ {
		q := url.Values{"limit": {strconv.Itoa(issuesPerPage)}, "page": {strconv.Itoa(page)}}

		var batch []comment
		if err := a.doJSON(ctx, http.MethodGet, path+"/comments?"+q.Encode(), nil, &batch); err != nil {
			return nil, fmt.Errorf("forgejo: list comments of issue %s: %w", issueID, err)
		}
		if len(batch) == 0 {
			break
		}
		for _, c := range batch {
			if c.Body == "" {
				continue
			}
			author := ""
			if c.User != nil {
				author = c.User.Login
			}
			out = append(out, tracker.Comment{ID: strconv.Itoa(c.ID), Author: author, Body: c.Body, CreatedAt: c.CreatedAt}.Clean())
		}
	}

	slices.SortStableFunc(out, func(x, y tracker.Comment) int { return x.CreatedAt.Compare(y.CreatedAt) })

	return out, nil
}
