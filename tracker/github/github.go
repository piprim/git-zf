package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	gogithub "github.com/google/go-github/v73/github"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

const (
	trackerType   = "github"
	statusOpen    = "open"
	statusClosed  = "closed"
	issuesPerPage = 100
)

type githubAdapter struct {
	client *gogithub.Client
	cfg    config.IssueTrackerConfig
}

// New creates a GitHub adapter from cfg.
func New(cfg config.IssueTrackerConfig) (tracker.Tracker, error) {
	if cfg.Token == "" {
		return nil, errors.New("github: token is required")
	}

	c := gogithub.NewClient(nil).WithAuthToken(cfg.Token)
	if url := strings.TrimRight(cfg.URL, "/"); url != "" && url != "https://api.github.com" && url != "https://github.com" {
		var err error

		c, err = c.WithEnterpriseURLs(cfg.URL, cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("github: enterprise URL %q: %w", cfg.URL, err)
		}
	}

	return &githubAdapter{client: c, cfg: cfg}, nil
}

// ListIssues fetches open issues assigned to the authenticated user across all
// accessible repositories, paginates through every page, drops pull requests,
// and applies the optional client-side Projects filter.
func (a *githubAdapter) ListIssues(ctx context.Context) ([]tracker.Issue, error) {
	opt := &gogithub.IssueListOptions{
		Filter:      "assigned",
		State:       statusOpen,
		ListOptions: gogithub.ListOptions{PerPage: issuesPerPage},
	}

	var out []tracker.Issue

	for {
		page, resp, err := a.client.Issues.List(ctx, true, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list issues: %w", err)
		}

		for _, iss := range page {
			if iss.IsPullRequest() {
				continue
			}

			proj := iss.GetRepository().GetFullName()
			if len(a.cfg.Projects) > 0 && !slices.Contains(a.cfg.IDs(), proj) {
				continue
			}

			out = append(out, toIssue(iss, proj))
		}

		if resp.NextPage == 0 {
			break
		}

		opt.ListOptions.Page = resp.NextPage
	}

	return out, nil
}

// toIssue converts a GitHub issue of project (an "owner/repo").
func toIssue(iss *gogithub.Issue, project string) tracker.Issue {
	labels := make([]string, len(iss.Labels))
	for i, l := range iss.Labels {
		labels[i] = l.GetName()
	}

	return tracker.Issue{
		TrackerType: trackerType,
		ID:          strconv.Itoa(iss.GetNumber()),
		Subject:     iss.GetTitle(),
		Description: iss.GetBody(),
		Status:      iss.GetState(),
		Project:     project,
		Labels:      labels,
		CreatedAt:   iss.GetCreatedAt().Time,
		UpdatedAt:   iss.GetUpdatedAt().Time,
		Closed:      iss.GetState() == statusClosed,
	}.Clean()
}

// ListProjectIssues fetches every open issue of the configured repository,
// whoever it is assigned to, page after page, and drops pull requests.
func (a *githubAdapter) ListProjectIssues(ctx context.Context) ([]tracker.Issue, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return nil, err
	}

	opt := &gogithub.IssueListByRepoOptions{
		State:       statusOpen,
		ListOptions: gogithub.ListOptions{PerPage: issuesPerPage},
	}

	var out []tracker.Issue

	for {
		page, resp, err := a.client.Issues.ListByRepo(ctx, owner, repo, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list issues of %s/%s: %w", owner, repo, err)
		}

		for _, iss := range page {
			if !iss.IsPullRequest() {
				out = append(out, toIssue(iss, owner+"/"+repo))
			}
		}

		if resp.NextPage == 0 {
			break
		}

		opt.ListOptions.Page = resp.NextPage
	}

	return out, nil
}

// CreateIssue creates an issue in the configured repository.
func (a *githubAdapter) CreateIssue(ctx context.Context, title, description string) (tracker.Issue, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return tracker.Issue{}, err
	}

	iss, _, err := a.client.Issues.Create(ctx, owner, repo,
		&gogithub.IssueRequest{Title: gogithub.Ptr(title), Body: gogithub.Ptr(description)})
	if err != nil {
		return tracker.Issue{}, fmt.Errorf("github: create issue: %w", err)
	}

	return toIssue(iss, owner+"/"+repo), nil
}

// SetIssueOpen reopens (open) or closes the issue.
func (a *githubAdapter) SetIssueOpen(ctx context.Context, issueID string, open bool) error {
	state := statusClosed
	if open {
		state = statusOpen
	}

	return a.UpdateIssueStatus(ctx, issueID, state)
}

// ListStatuses returns the static set of GitHub issue states (open, closed).
func (*githubAdapter) ListStatuses(_ context.Context) ([]string, error) {
	return []string{statusOpen, statusClosed}, nil
}

// ownerRepo resolves the single "owner/repo" entry from cfg.Projects.
// It returns an error when Projects does not contain exactly one valid entry.
func (a *githubAdapter) ownerRepo() (owner, repo string, err error) {
	if len(a.cfg.Projects) != 1 {
		return "", "", errors.New("github: exactly one project must be configured (got " + strconv.Itoa(len(a.cfg.Projects)) + ")")
	}

	owner, repo, ok := strings.Cut(a.cfg.Projects[0].ID, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", fmt.Errorf("github: invalid project %q (expected owner/repo)", a.cfg.Projects[0].ID)
	}

	return owner, repo, nil
}

// UpdateIssueStatus toggles the issue's state to "open" or "closed" via
// PATCH /repos/{owner}/{repo}/issues/{number}. The owner/repo is taken from
// cfg.Projects, which must contain exactly one "owner/repo" entry.
func (a *githubAdapter) UpdateIssueStatus(ctx context.Context, issueID, statusName string) error {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return err
	}

	state, err := mapState(statusName)
	if err != nil {
		return err
	}

	n, err := strconv.Atoi(issueID)
	if err != nil {
		return fmt.Errorf("github: invalid issue id %q: %w", issueID, err)
	}

	_, _, err = a.client.Issues.Edit(ctx, owner, repo, n, &gogithub.IssueRequest{State: gogithub.Ptr(state)})
	if err != nil {
		return fmt.Errorf("github: edit issue %d: %w", n, err)
	}

	return nil
}

// IsIssueClosed asks GitHub for the issue's state. The id is the issue number
// as a decimal string. HTTP 404 → tracker.ErrIssueNotFound; other failures
// are wrapped.
func (a *githubAdapter) IsIssueClosed(ctx context.Context, issueID string) (bool, error) {
	iss, err := a.GetIssue(ctx, issueID)

	return iss.Closed, err
}

func mapState(name string) (string, error) {
	switch name {
	case statusOpen, statusClosed:
		return name, nil
	default:
		return "", fmt.Errorf("github: unknown status %q (want %q or %q)", name, statusOpen, statusClosed)
	}
}

// AddComment posts body as an issue comment via
// POST /repos/{owner}/{repo}/issues/{number}/comments.
func (a *githubAdapter) AddComment(ctx context.Context, issueID, body string) (tracker.Comment, error) {
	owner, repo, n, err := a.issueRef(issueID)
	if err != nil {
		return tracker.Comment{}, err
	}

	c, _, err := a.client.Issues.CreateComment(ctx, owner, repo, n, &gogithub.IssueComment{Body: gogithub.Ptr(body)})
	if err != nil {
		return tracker.Comment{}, fmt.Errorf("github: comment on issue %d: %w", n, err)
	}

	return tracker.Comment{ID: strconv.FormatInt(c.GetID(), 10), Author: c.GetUser().GetLogin(), Body: c.GetBody(), CreatedAt: c.GetCreatedAt().Time}.Clean(), nil
}

// issueRef resolves the configured repository and the issue number.
func (a *githubAdapter) issueRef(issueID string) (owner, repo string, n int, err error) {
	owner, repo, err = a.ownerRepo()
	if err != nil {
		return "", "", 0, err
	}
	n, err = strconv.Atoi(issueID)
	if err != nil {
		return "", "", 0, fmt.Errorf("github: invalid issue id %q: %w", issueID, err)
	}

	return owner, repo, n, nil
}

// GetIssue reads one issue. HTTP 404 → tracker.ErrIssueNotFound.
func (a *githubAdapter) GetIssue(ctx context.Context, issueID string) (tracker.Issue, error) {
	owner, repo, n, err := a.issueRef(issueID)
	if err != nil {
		return tracker.Issue{}, err
	}

	iss, resp, err := a.client.Issues.Get(ctx, owner, repo, n)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return tracker.Issue{}, tracker.ErrIssueNotFound
		}

		return tracker.Issue{}, fmt.Errorf("github: get issue %s: %w", issueID, err)
	}

	return toIssue(iss, owner+"/"+repo), nil
}

// UpdateIssue sets title and body.
func (a *githubAdapter) UpdateIssue(ctx context.Context, issueID, title, description string) error {
	owner, repo, n, err := a.issueRef(issueID)
	if err != nil {
		return err
	}

	_, _, err = a.client.Issues.Edit(ctx, owner, repo, n,
		&gogithub.IssueRequest{Title: gogithub.Ptr(title), Body: gogithub.Ptr(description)})
	if err != nil {
		return fmt.Errorf("github: edit issue %d: %w", n, err)
	}

	return nil
}

// ListLabels returns the names of the repository's labels, every page.
func (a *githubAdapter) ListLabels(ctx context.Context) ([]string, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return nil, err
	}

	var names []string
	opt := &gogithub.ListOptions{PerPage: issuesPerPage}
	for {
		page, resp, err := a.client.Issues.ListLabels(ctx, owner, repo, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list labels: %w", err)
		}
		for _, l := range page {
			names = append(names, l.GetName())
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return names, nil
}

// defaultLabelColor is the colour of a label git-zf creates (GitHub wants it
// without the '#').
const defaultLabelColor = "ededed"

// SetLabels replaces the issue's labels with names, creating the ones the
// repository lacks.
func (a *githubAdapter) SetLabels(ctx context.Context, issueID string, names []string) error {
	owner, repo, n, err := a.issueRef(issueID)
	if err != nil {
		return err
	}

	existing, err := a.ListLabels(ctx)
	if err != nil {
		return err
	}
	// GitHub label names are case-insensitive: an existing one is sent in its
	// own spelling, never created again.
	sent := make([]string, 0, len(names))
	for _, name := range names {
		if i := slices.IndexFunc(existing, func(e string) bool { return strings.EqualFold(e, name) }); i >= 0 {
			sent = append(sent, existing[i])

			continue
		}
		_, _, err := a.client.Issues.CreateLabel(ctx, owner, repo,
			&gogithub.Label{Name: gogithub.Ptr(name), Color: gogithub.Ptr(defaultLabelColor)})
		if err != nil {
			return fmt.Errorf("github: create label %q: %w", name, err)
		}
		sent = append(sent, name)
	}

	if _, _, err := a.client.Issues.ReplaceLabelsForIssue(ctx, owner, repo, n, sent); err != nil {
		return fmt.Errorf("github: set labels of issue %d: %w", n, err)
	}

	return nil
}

// ListComments returns the issue's comments, oldest first; a comment with an
// empty body is dropped.
func (a *githubAdapter) ListComments(ctx context.Context, issueID string) ([]tracker.Comment, error) {
	owner, repo, n, err := a.issueRef(issueID)
	if err != nil {
		return nil, err
	}

	var out []tracker.Comment
	opt := &gogithub.IssueListCommentsOptions{
		Sort: gogithub.Ptr("created"), Direction: gogithub.Ptr("asc"),
		ListOptions: gogithub.ListOptions{PerPage: issuesPerPage},
	}
	for {
		page, resp, err := a.client.Issues.ListComments(ctx, owner, repo, n, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list comments of issue %d: %w", n, err)
		}
		for _, c := range page {
			if c.GetBody() == "" {
				continue
			}
			out = append(out, tracker.Comment{
				ID: strconv.FormatInt(c.GetID(), 10), Author: c.GetUser().GetLogin(),
				Body: c.GetBody(), CreatedAt: c.GetCreatedAt().Time,
			}.Clean())
		}
		if resp.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = resp.NextPage
	}

	return out, nil
}
