package forgejo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/piprim/git-zf/tracker"
)

// Compile-time check: Forgejo is a forge.
var _ tracker.PullRequests = (*forgejoAdapter)(nil)

// pullRequest is the subset of the Forgejo PullRequest object we consume.
type pullRequest struct {
	Number int `json:"number"`
	//nolint:tagliatelle // Forgejo wire format
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref  string      `json:"ref"`
		Repo *repository `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (a *forgejoAdapter) pullsPath() (path, project string, err error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return "", "", err
	}

	return "/repos/" + owner + "/" + repo + "/pulls", owner + "/" + repo, nil
}

// FindPullRequest walks the open pull requests of the repository and returns
// the one whose head is branch in this repository (the API has no head
// filter; a fork's pull request with the same ref is not ours, nor is one
// whose head repository was deleted: its head.repo is null).
func (a *forgejoAdapter) FindPullRequest(ctx context.Context, branch string) (tracker.PullRequest, error) {
	path, project, err := a.pullsPath()
	if err != nil {
		return tracker.PullRequest{}, err
	}

	q := url.Values{}
	q.Set("state", statusOpen)
	q.Set("limit", strconv.Itoa(issuesPerPage))

	for page := 1; page <= maxPages; page++ {
		q.Set("page", strconv.Itoa(page))

		var batch []pullRequest
		if err := a.doJSON(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &batch); err != nil {
			return tracker.PullRequest{}, fmt.Errorf("forgejo: list pull requests: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			pr := &batch[i]
			if pr.Head.Ref == branch && pr.Head.Repo != nil && strings.EqualFold(pr.Head.Repo.FullName, project) {
				return toPullRequest(pr), nil
			}
		}
	}

	return tracker.PullRequest{}, tracker.ErrNoPullRequest
}

// CreatePullRequest opens a pull request of head against base.
func (a *forgejoAdapter) CreatePullRequest(ctx context.Context, head, base, title, body string) (tracker.PullRequest, error) {
	path, _, err := a.pullsPath()
	if err != nil {
		return tracker.PullRequest{}, err
	}

	req := struct {
		Head  string `json:"head"`
		Base  string `json:"base"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Head: head, Base: base, Title: title, Body: body}

	var created pullRequest
	if err := a.doJSON(ctx, http.MethodPost, path, req, &created); err != nil {
		return tracker.PullRequest{}, fmt.Errorf("forgejo: create pull request for %q: %w", head, err)
	}

	return toPullRequest(&created), nil
}

// MergePullRequest merges the pull request with method via
// POST /repos/{owner}/{repo}/pulls/{index}/merge, provided its head is still
// head (head_commit_id). A refusal (405, 409) carries the API's message.
func (a *forgejoAdapter) MergePullRequest(ctx context.Context, number string, method tracker.MergeMethod, head string) error {
	path, _, err := a.pullsPath()
	if err != nil {
		return err
	}
	if _, err := strconv.Atoi(number); err != nil {
		return fmt.Errorf("forgejo: invalid pull request number %q: %w", number, err)
	}

	req := struct {
		Do           string `json:"Do"`             //nolint:tagliatelle // Forgejo wire format
		HeadCommitID string `json:"head_commit_id"` //nolint:tagliatelle // Forgejo wire format
	}{Do: string(method), HeadCommitID: head}
	if err := a.doJSON(ctx, http.MethodPost, path+"/"+number+"/merge", req, nil); err != nil {
		return fmt.Errorf("forgejo: merge pull request #%s: %w", number, err)
	}

	return nil
}

func toPullRequest(pr *pullRequest) tracker.PullRequest {
	return tracker.PullRequest{
		Number: strconv.Itoa(pr.Number),
		URL:    pr.HTMLURL,
		Head:   pr.Head.Ref,
		Base:   pr.Base.Ref,
	}.Clean()
}
