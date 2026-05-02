package redmine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

type issue struct {
	ID          int     `json:"id"`
	Subject     string  `json:"subject"`
	Description string  `json:"description"`
	Status      *status `json:"status"`
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
			return fmt.Errorf("status %q not found in Redmine; check in_progress_status in .git-zf.json", statusNameOrID)
		}
	}

	type issueUpdate struct {
		//nolint:tagliatelle // Redmine need it
		StatusID int `json:"status_id"`
	}
	type body struct {
		Issue issueUpdate `json:"issue"`
	}

	payload, err := json.Marshal(body{Issue: issueUpdate{StatusID: statusID}})
	if err != nil {
		return fmt.Errorf("marshal status update: %w", err)
	}

	base := strings.TrimRight(a.cfg.URL, "/")
	url := fmt.Sprintf("%s/issues/%s.json", base, issueID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build status update request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Redmine-API-Key", a.cfg.Token)

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("update issue %s status: %w", issueID, err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		msgErr, err := io.ReadAll(resp.Body)
		if err != nil {
			msgErr = []byte("unreachable content")
		}
		format := `update issue %s status: unexpected HTTP %d with content: "%s"`

		return fmt.Errorf(format, issueID, resp.StatusCode, string(msgErr))
	}

	return nil
}
