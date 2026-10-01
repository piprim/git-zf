package tracker

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/config"
)

// Issue is the tracker-agnostic wire shape of a work item: every field is a
// string exactly as a tracker backend reports it. It is the external/source
// representation, the first of three "Issue" shapes — it is embedded into the
// in-flow domain entity issue.Issue, which is in turn persisted as store.Issue.
// See the doc on issue.Issue for the full picture.
type Issue struct {
	TrackerType string
	ID          string
	Subject     string
	Description string
	Status      string
	Project     string
}

// ErrIssueNotFound is returned by IsIssueClosed when the tracker has no record
// of the requested issueID (HTTP 404 or equivalent). Callers can branch on it
// via errors.Is to distinguish "tracker says open" from "tracker doesn't know".
var ErrIssueNotFound = errors.New("tracker: issue not found")

// Tracker is the contract every adapter must satisfy.
type Tracker interface {
	// ListIssues retrieves the issues from the tracker
	ListIssues(ctx context.Context) ([]Issue, error)
	// ListStatuses returns the available status names for the tracker.
	ListStatuses(ctx context.Context) ([]string, error)
	// UpdateIssueStatus updates the status from the given issueID
	UpdateIssueStatus(ctx context.Context, issueID, statusName string) error
	// IsIssueClosed reports whether the tracker considers issueID closed.
	// Returns ErrIssueNotFound for missing-issue cases so callers can format
	// the warning distinctly from transport/auth failures.
	IsIssueClosed(ctx context.Context, issueID string) (bool, error)
	// AddComment posts body as a comment on issueID.
	AddComment(ctx context.Context, issueID, body string) error
}

var registry = make(map[string]func(config.IssueTrackerConfig) (Tracker, error))

// Register adds a factory function for the named tracker type. It is meant to
// be called from an adapter's init(), before any New; it is not safe to call
// concurrently with New.
func Register(name string, fn func(config.IssueTrackerConfig) (Tracker, error)) {
	registry[name] = fn
}

// New constructs a Tracker from cfg using the registered factory.
func New(cfg config.IssueTrackerConfig) (Tracker, error) {
	fn, ok := registry[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown tracker type %q: adapter not registered", cfg.Type)
	}

	return fn(cfg)
}
