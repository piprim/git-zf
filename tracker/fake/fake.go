// Package fake provides an in-process tracker.Tracker for tests.
// It registers itself under type "fake" so close_e2e_test.go can wire it
// through config.IssueTrackerConfig like a real adapter.
package fake

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

func init() {
	tracker.Register("fake", New)
}

// Tracker is the exposed concrete type so tests can read RecordedUpdates
// after Close() returns. Construct via New() to satisfy the registry signature.
type Tracker struct {
	mu               sync.Mutex
	Issues           []tracker.Issue
	Statuses         []string
	RecordedUpdates  []Update
	RecordedComments []Comment

	// Closed[id] == true → IsIssueClosed returns (true, nil) for id.
	// Default zero-value (absent or false) → IsIssueClosed returns (false, nil).
	Closed map[string]bool
	// Unknown[id] == true → IsIssueClosed returns (false, tracker.ErrIssueNotFound).
	Unknown map[string]bool
	// Errors[id] != nil → IsIssueClosed returns (false, Errors[id]) — for transport-error scenarios.
	Errors map[string]error

	// ProjectIssues is what ListProjectIssues returns: the project's open
	// issues. CreateIssue, SetIssueOpen, CloseIssue and ReopenIssue keep it
	// and Closed in step, so the fake behaves like one tracker over time.
	ProjectIssues []tracker.Issue
	// ListErr and CreateErr, when non-nil, are returned by ListProjectIssues
	// and CreateIssue.
	ListErr, CreateErr error
	// ListProjectCalls counts the ListProjectIssues calls.
	ListProjectCalls int
	RecordedCreates  []Create
	RecordedOpens    []Open
	// ClosingStatuses are the status names that close an issue when
	// UpdateIssueStatus applies them. Any other name becomes the issue's
	// listed status.
	ClosingStatuses []string

	nextID  int
	shelved map[string]tracker.Issue // closed issues, by ID
}

// Update captures one UpdateIssueStatus call.
type Update struct {
	IssueID    string
	StatusName string
}

type Comment struct {
	IssueID string
	Body    string
}

// New is the tracker.Register factory. cfg is ignored — tests configure the
// returned *Tracker directly via field access.
func New(_ config.IssueTrackerConfig) (tracker.Tracker, error) {
	return &Tracker{
		Statuses: []string{"In Progress", "Closed"},
	}, nil
}

// ListIssues returns a snapshot of the configured issues.
func (t *Tracker) ListIssues(_ context.Context) ([]tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]tracker.Issue, len(t.Issues))
	copy(out, t.Issues)

	return out, nil
}

// ListStatuses returns a snapshot of the configured status names.
func (t *Tracker) ListStatuses(_ context.Context) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]string, len(t.Statuses))
	copy(out, t.Statuses)

	return out, nil
}

// IsIssueClosed consults Closed / Unknown / Errors in that priority order.
func (t *Tracker) IsIssueClosed(_ context.Context, issueID string) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err, ok := t.Errors[issueID]; ok && err != nil {
		return false, err
	}

	if t.Unknown[issueID] {
		return false, tracker.ErrIssueNotFound
	}

	return t.Closed[issueID], nil
}

// UpdateIssueStatus records the call so tests can assert on it.
func (t *Tracker) UpdateIssueStatus(_ context.Context, issueID, statusName string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.RecordedUpdates = append(t.RecordedUpdates, Update{IssueID: issueID, StatusName: statusName})

	if slices.Contains(t.ClosingStatuses, statusName) {
		t.setOpen(issueID, false)

		return nil
	}

	for i := range t.ProjectIssues {
		if t.ProjectIssues[i].ID == issueID {
			t.ProjectIssues[i].Status = statusName
		}
	}

	return nil
}

func (t *Tracker) AddComment(_ context.Context, issueID, body string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.RecordedComments = append(t.RecordedComments, Comment{IssueID: issueID, Body: body})

	return nil
}

// Create captures one CreateIssue call.
type Create struct {
	Title, Description string
}

// Open captures one SetIssueOpen call.
type Open struct {
	IssueID string
	Open    bool
}

// ListProjectIssues returns a snapshot of the open issues.
func (t *Tracker) ListProjectIssues(_ context.Context) ([]tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ListProjectCalls++
	if t.ListErr != nil {
		return nil, t.ListErr
	}

	return slices.Clone(t.ProjectIssues), nil
}

// CreateIssue records the call and adds an open issue numbered 1, 2, 3….
func (t *Tracker) CreateIssue(_ context.Context, title, description string) (tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.CreateErr != nil {
		return tracker.Issue{}, t.CreateErr
	}

	t.nextID++
	iss := tracker.Issue{
		TrackerType: "fake", ID: strconv.Itoa(t.nextID), Subject: title, Description: description,
		Status: "open", CreatedAt: time.Now().UTC(),
	}
	t.RecordedCreates = append(t.RecordedCreates, Create{Title: title, Description: description})
	t.ProjectIssues = append(t.ProjectIssues, iss)

	return iss, nil
}

// SetIssueOpen records the call and opens or closes the issue.
func (t *Tracker) SetIssueOpen(_ context.Context, issueID string, open bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.RecordedOpens = append(t.RecordedOpens, Open{IssueID: issueID, Open: open})
	t.setOpen(issueID, open)

	return nil
}

// CloseIssue closes the issue the way a person does in the tracker's own UI:
// nothing is recorded.
func (t *Tracker) CloseIssue(issueID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.setOpen(issueID, false)
}

// ReopenIssue is the counterpart of CloseIssue.
func (t *Tracker) ReopenIssue(issueID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.setOpen(issueID, true)
}

// setOpen moves the issue between ProjectIssues and the closed shelf. The
// caller holds t.mu.
func (t *Tracker) setOpen(issueID string, open bool) {
	if t.Closed == nil {
		t.Closed = make(map[string]bool)
	}
	if t.shelved == nil {
		t.shelved = make(map[string]tracker.Issue)
	}

	t.Closed[issueID] = !open

	i := slices.IndexFunc(t.ProjectIssues, func(iss tracker.Issue) bool { return iss.ID == issueID })
	switch {
	case !open && i >= 0:
		t.shelved[issueID] = t.ProjectIssues[i]
		t.ProjectIssues = slices.Delete(t.ProjectIssues, i, i+1)
	case open && i < 0:
		if iss, ok := t.shelved[issueID]; ok {
			t.ProjectIssues = append(t.ProjectIssues, iss)
			delete(t.shelved, issueID)
		}
	}
}
