package github

import (
	"context"
	"errors"
	"fmt"
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
	client   *gogithub.Client
	cfg      config.IssueTrackerConfig
	projects map[string]struct{}
}

// New creates a GitHub adapter from cfg.
func New(cfg config.IssueTrackerConfig) (tracker.Tracker, error) {
	if cfg.Token == "" {
		return nil, errors.New("github: token is required")
	}

	c := gogithub.NewClient(nil).WithAuthToken(cfg.Token)
	if cfg.URL != "" && cfg.URL != "https://api.github.com" {
		var err error

		c, err = c.WithEnterpriseURLs(cfg.URL, cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("github: enterprise URL %q: %w", cfg.URL, err)
		}
	}

	return &githubAdapter{
		client:   c,
		cfg:      cfg,
		projects: toProjectSet(cfg.Projects),
	}, nil
}

// toProjectSet builds a lookup set from cfg.Projects. Returns nil when the
// slice is empty so callers can short-circuit the filter.
func toProjectSet(list []string) map[string]struct{} {
	if len(list) == 0 {
		return nil
	}

	out := make(map[string]struct{}, len(list))
	for _, p := range list {
		out[p] = struct{}{}
	}

	return out
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
			if a.projects != nil {
				if _, ok := a.projects[proj]; !ok {
					continue
				}
			}

			out = append(out, tracker.Issue{
				TrackerType: trackerType,
				ID:          strconv.Itoa(iss.GetNumber()),
				Subject:     iss.GetTitle(),
				Description: iss.GetBody(),
				Status:      iss.GetState(),
				Project:     proj,
			})
		}

		if resp.NextPage == 0 {
			break
		}

		opt.ListOptions.Page = resp.NextPage
	}

	return out, nil
}

// ListStatuses returns the static set of GitHub issue states (open, closed).
func (*githubAdapter) ListStatuses(_ context.Context) ([]string, error) {
	return []string{statusOpen, statusClosed}, nil
}

// UpdateIssueStatus toggles the issue's state to "open" or "closed" via
// PATCH /repos/{owner}/{repo}/issues/{number}. The owner/repo is taken from
// cfg.Projects, which must contain exactly one "owner/repo" entry.
func (a *githubAdapter) UpdateIssueStatus(ctx context.Context, issueID, statusName string) error {
	if len(a.cfg.Projects) != 1 {
		return errors.New("github: UpdateIssueStatus requires exactly one project configured")
	}

	owner, repo, ok := strings.Cut(a.cfg.Projects[0], "/")
	if !ok || owner == "" || repo == "" {
		return fmt.Errorf("github: invalid project %q (expected owner/repo)", a.cfg.Projects[0])
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

func mapState(name string) (string, error) {
	switch name {
	case statusOpen, statusClosed:
		return name, nil
	default:
		return "", fmt.Errorf("github: unknown status %q (want %q or %q)", name, statusOpen, statusClosed)
	}
}
