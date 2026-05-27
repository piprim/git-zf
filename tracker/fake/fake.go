// Package fake provides an in-process tracker.Tracker for tests.
// It registers itself under type "fake" so close_e2e_test.go can wire it
// through config.IssueTrackerConfig like a real adapter.
package fake

import (
	"context"
	"sync"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

func init() {
	tracker.Register("fake", New)
}

// Tracker is the exposed concrete type so tests can read RecordedUpdates
// after Close() returns. Construct via New() to satisfy the registry signature.
type Tracker struct {
	mu              sync.Mutex
	Issues          []tracker.Issue
	Statuses        []string
	RecordedUpdates []Update

	// Closed[id] == true → IsIssueClosed returns (true, nil) for id.
	// Default zero-value (absent or false) → IsIssueClosed returns (false, nil).
	Closed map[string]bool
	// Unknown[id] == true → IsIssueClosed returns (false, tracker.ErrIssueNotFound).
	Unknown map[string]bool
	// Errors[id] != nil → IsIssueClosed returns (false, Errors[id]) — for transport-error scenarios.
	Errors map[string]error
}

// Update captures one UpdateIssueStatus call.
type Update struct {
	IssueID    string
	StatusName string
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

	return nil
}
