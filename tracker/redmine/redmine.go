package redmine

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
	"strconv"
	"strings"

	"github.com/mattn/go-redmine"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

const trackerType = "redmine"

type status struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type project struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
}

type issue struct {
	ID          int      `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	Status      *status  `json:"status"`
	Project     *project `json:"project"`
}

type issuesResponse struct {
	Issues []issue `json:"issues"`
	//nolint:tagliatelle // Needed by redmine
	TotalCount int `json:"total_count"`
}

type redmineAdapter struct {
	client *redmine.Client
	cfg    config.IssueTrackerConfig
	http   *http.Client
}

// New creates a Redmine adapter from cfg.
func New(cfg config.IssueTrackerConfig) (tracker.Tracker, error) {
	if cfg.URL == "" {
		return nil, errors.New("redmine: URL is required")
	}
	if cfg.Token == "" {
		return nil, errors.New("redmine: Token is required")
	}

	c := redmine.NewClient(cfg.URL, cfg.Token)

	return &redmineAdapter{client: c, cfg: cfg, http: &http.Client{}}, nil
}

// ListIssues fetches open issues. With cfg.Projects set, it asks each project
// (a slug or a numeric ID) for its open issues, whoever they are assigned to.
// Without projects, it fetches the open issues assigned to the authenticated
// user across the whole tracker.
func (a *redmineAdapter) ListIssues(ctx context.Context) ([]tracker.Issue, error) {
	if len(a.cfg.Projects) == 0 {
		return a.fetchIssues(ctx, "/issues.json?assigned_to_id=me&status_id=open&limit=100", "")
	}

	var out []tracker.Issue

	for _, p := range a.cfg.Projects {
		issues, err := a.fetchIssues(ctx, "/projects/"+url.PathEscape(p)+"/issues.json?status_id=open&limit=100", p)
		if err != nil {
			return nil, fmt.Errorf("project %q: %w", p, err)
		}

		out = append(out, issues...)
	}

	return out, nil
}

// fetchIssues GETs one page of issues from path. Every issue is reported under
// project; when project is empty, the name comes from the issue itself.
//
// ponytail: one page of 100; walk offset/total_count if a listing outgrows it.
func (a *redmineAdapter) fetchIssues(ctx context.Context, path, project string) ([]tracker.Issue, error) {
	endpoint := strings.TrimRight(a.cfg.URL, "/") + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build redmine issues request: %w", err)
	}

	req.Header.Set("X-Redmine-API-Key", a.cfg.Token)

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch redmine issues: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch redmine issues: unexpected status %d", resp.StatusCode)
	}

	var payload issuesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode redmine issues: %w", err)
	}

	result := make([]tracker.Issue, 0, len(payload.Issues))
	for _, iss := range payload.Issues {
		statusName := ""
		if iss.Status != nil {
			statusName = iss.Status.Name
		}

		result = append(result, tracker.Issue{
			TrackerType: trackerType,
			ID:          strconv.Itoa(iss.ID),
			Subject:     iss.Subject,
			Description: iss.Description,
			Status:      statusName,
			Project:     cmp.Or(project, redmineProjectName(iss.Project)),
		})
	}

	return result, nil
}

// ListStatuses returns every available status name from GET /issue_statuses.json.
func (a *redmineAdapter) ListStatuses(_ context.Context) ([]string, error) {
	statuses, err := a.client.IssueStatuses()
	if err != nil {
		return nil, fmt.Errorf("fetch issue statuses: %w", err)
	}

	names := make([]string, len(statuses))
	for i, s := range statuses {
		names[i] = s.Name
	}

	return names, nil
}

// UpdateIssueStatus resolves statusName via GET /issue_statuses.json then PUTs
// only the status_id. We send a minimal payload instead of using go-redmine's
// UpdateIssue because that serializes every Issue field (including category_id:0
// without omitempty), which triggers Redmine validation errors on issues with no
// category assigned.
func (a *redmineAdapter) UpdateIssueStatus(ctx context.Context, issueID, statusNameOrID string) error {
	statuses, err := a.client.IssueStatuses()
	if err != nil {
		return fmt.Errorf("fetch issue statuses: %w", err)
	}

	var statusID int
	found := false

	for _, s := range statuses {
		if !strings.EqualFold(s.Name, statusNameOrID) {
			continue
		}

		statusID = s.Id
		found = true

		break
	}

	if !found {
		statusID, err = strconv.Atoi(statusNameOrID)
		if err != nil {
			return fmt.Errorf("status %q not found in Redmine", statusNameOrID)
		}
	}

	type issueUpdate struct {
		//nolint:tagliatelle // Redmine need it
		StatusID int `json:"status_id"`
	}
	type body struct {
		Issue issueUpdate `json:"issue"`
	}

	return a.putIssue(ctx, issueID, "status", body{Issue: issueUpdate{StatusID: statusID}})
}

// AddComment adds body as a journal note via PUT /issues/{id}.json with
// {"issue":{"notes": body}} — Redmine's comment mechanism.
func (a *redmineAdapter) AddComment(ctx context.Context, issueID, body string) error {
	type issueNotes struct {
		Notes string `json:"notes"`
	}
	type payload struct {
		Issue issueNotes `json:"issue"`
	}

	return a.putIssue(ctx, issueID, "comment", payload{Issue: issueNotes{Notes: body}})
}

// putIssue JSON-encodes payload and PUTs it to /issues/{id}.json. what names
// the operation in error messages ("status", "comment").
func (a *redmineAdapter) putIssue(ctx context.Context, issueID, what string, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s update: %w", what, err)
	}

	base := strings.TrimRight(a.cfg.URL, "/")
	endpoint := fmt.Sprintf("%s/issues/%s.json", base, issueID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build %s update request: %w", what, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Redmine-API-Key", a.cfg.Token)

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("update issue %s %s: %w", issueID, what, err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		msgErr, err := io.ReadAll(resp.Body)
		if err != nil {
			msgErr = []byte("unreachable content")
		}
		format := `update issue %s %s: unexpected HTTP %d with content: "%s"`

		return fmt.Errorf(format, issueID, what, resp.StatusCode, string(msgErr))
	}

	return nil
}

// IsIssueClosed asks Redmine for the issue and reads status.is_closed.
// HTTP 404 → tracker.ErrIssueNotFound; other failures are wrapped.
func (a *redmineAdapter) IsIssueClosed(ctx context.Context, issueID string) (bool, error) {
	base := strings.TrimRight(a.cfg.URL, "/")
	endpoint := fmt.Sprintf("%s/issues/%s.json", base, issueID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("redmine: build request: %w", err)
	}

	req.Header.Set("X-Redmine-API-Key", a.cfg.Token)

	resp, err := a.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("redmine: get issue %s: %w", issueID, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return false, tracker.ErrIssueNotFound
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("redmine: get issue %s: unexpected status %d", issueID, resp.StatusCode)
	}

	var payload struct {
		Issue struct {
			Status struct {
				IsClosed bool `json:"is_closed"`
			} `json:"status"`
		} `json:"issue"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, fmt.Errorf("redmine: decode issue %s: %w", issueID, err)
	}

	return payload.Issue.Status.IsClosed, nil
}

// redmineProjectName picks the slug, then the display name, then the numeric
// ID as a last resort; returns "" when the project is omitted from the response.
func redmineProjectName(p *project) string {
	if p == nil {
		return ""
	}
	if p.Identifier != "" {
		return p.Identifier
	}
	if p.Name != "" {
		return p.Name
	}

	return strconv.Itoa(p.ID)
}
