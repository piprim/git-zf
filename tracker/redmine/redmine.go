package redmine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	redminelib "github.com/mattn/go-redmine"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

const trackerType = "redmine"

type issuesResponse struct {
	Issues []struct {
		ID          int    `json:"id"`
		Subject     string `json:"subject"`
		Description string `json:"description"`
		Status      *struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"status"`
	} `json:"issues"`
	TotalCount int `json:"total_count"`
}

type redmineAdapter struct {
	client *redminelib.Client
	cfg    config.IssueTrackerConfig
	http   *http.Client
}

// New creates a Redmine adapter from cfg.
func New(cfg config.IssueTrackerConfig) (tracker.Tracker, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("redmine: URL is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("redmine: Token is required")
	}

	c := redminelib.NewClient(cfg.URL, cfg.Token)

	return &redmineAdapter{client: c, cfg: cfg, http: &http.Client{}}, nil
}

// ListIssues fetches open issues assigned to the authenticated user.
func (a *redmineAdapter) ListIssues(ctx context.Context) ([]tracker.Issue, error) {
	base := strings.TrimRight(a.cfg.URL, "/")
	url := fmt.Sprintf("%s/issues.json?assigned_to_id=me&status_id=open&limit=100", base)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
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

	result := make([]tracker.Issue, len(payload.Issues))
	for i, iss := range payload.Issues {
		statusName := ""
		if iss.Status != nil {
			statusName = iss.Status.Name
		}

		result[i] = tracker.Issue{
			TrackerType: trackerType,
			ID:          strconv.Itoa(iss.ID),
			Subject:     iss.Subject,
			Description: iss.Description,
			Status:      statusName,
		}
	}

	return result, nil
}

// UpdateIssueStatus resolves statusName via GET /issue_statuses.json then PUTs
// the matching status_id. Match is case-insensitive.
// TODO: ctx is not forwarded to go-redmine calls — the library lacks context support.
func (a *redmineAdapter) UpdateIssueStatus(_ context.Context, issueID, statusName string) error {
	id, err := strconv.Atoi(issueID)
	if err != nil {
		return fmt.Errorf("invalid issue id %q: %w", issueID, err)
	}

	statuses, err := a.client.IssueStatuses()
	if err != nil {
		return fmt.Errorf("fetch issue statuses: %w", err)
	}

	var statusID int
	found := false

	for _, s := range statuses {
		if !strings.EqualFold(s.Name, statusName) {
			continue
		}

		statusID = s.Id
		found = true

		break
	}

	if !found {
		return fmt.Errorf("status %q not found in Redmine; check in_progress_status in .git-zf.json", statusName)
	}

	if err := a.client.UpdateIssue(redminelib.Issue{Id: id, StatusId: statusID}); err != nil {
		return fmt.Errorf("update issue %s status: %w", issueID, err)
	}

	return nil
}
