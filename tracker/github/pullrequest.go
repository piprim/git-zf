package github

import (
	"context"
	"fmt"
	"strconv"

	gogithub "github.com/google/go-github/v73/github"

	"github.com/piprim/git-zf/tracker"
)

// Compile-time check: GitHub is a forge.
var _ tracker.PullRequests = (*githubAdapter)(nil)

// FindPullRequest lists the open pull requests whose head is owner:branch
// and returns the first; GitHub allows one open pull request per head.
func (a *githubAdapter) FindPullRequest(ctx context.Context, branch string) (tracker.PullRequest, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return tracker.PullRequest{}, err
	}

	prs, _, err := a.client.PullRequests.List(ctx, owner, repo, &gogithub.PullRequestListOptions{
		State: statusOpen, Head: owner + ":" + branch, ListOptions: gogithub.ListOptions{PerPage: 1},
	})
	if err != nil {
		return tracker.PullRequest{}, fmt.Errorf("github: list pull requests of %q: %w", branch, err)
	}
	if len(prs) == 0 {
		return tracker.PullRequest{}, tracker.ErrNoPullRequest
	}

	return toPullRequest(prs[0]), nil
}

// CreatePullRequest opens a pull request of head against base.
func (a *githubAdapter) CreatePullRequest(ctx context.Context, head, base, title, body string) (tracker.PullRequest, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return tracker.PullRequest{}, err
	}

	pr, _, err := a.client.PullRequests.Create(ctx, owner, repo, &gogithub.NewPullRequest{
		Title: gogithub.Ptr(title), Head: gogithub.Ptr(head), Base: gogithub.Ptr(base), Body: gogithub.Ptr(body),
	})
	if err != nil {
		return tracker.PullRequest{}, fmt.Errorf("github: create pull request for %q: %w", head, err)
	}

	return toPullRequest(pr), nil
}

// MergePullRequest merges the pull request with method, provided its head is
// still head. GitHub answers 405 when it is not mergeable and 409 when the
// head moved; go-github returns both with the API's message.
func (a *githubAdapter) MergePullRequest(ctx context.Context, number string, method tracker.MergeMethod, head string) error {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(number)
	if err != nil {
		return fmt.Errorf("github: invalid pull request number %q: %w", number, err)
	}

	res, _, err := a.client.PullRequests.Merge(ctx, owner, repo, n, "", &gogithub.PullRequestOptions{MergeMethod: string(method), SHA: head})
	if err != nil {
		return fmt.Errorf("github: merge pull request #%d: %w", n, err)
	}
	if !res.GetMerged() {
		return fmt.Errorf("github: merge pull request #%d: %s", n, res.GetMessage())
	}

	return nil
}

func toPullRequest(pr *gogithub.PullRequest) tracker.PullRequest {
	return tracker.PullRequest{
		Number: strconv.Itoa(pr.GetNumber()),
		URL:    pr.GetHTMLURL(),
		Head:   pr.GetHead().GetRef(),
		Base:   pr.GetBase().GetRef(),
	}.Clean()
}
