package issue

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/tracker"
)

// Prompter is the sub-interface of cmd/issue.StartPrompter that this package
// uses to drive issue-input forms. It exists so GetFromUser and GetFromTracker
// can be invoked from tests without opening huh forms.
type Prompter interface {
	// PickIssueFromUser opens the manual issue-input form (issue ID, subject, type).
	PickIssueFromUser(ctx context.Context, allowedTypes []string) (*Issue, error)

	// PickIssueFromTracker opens the picker over a pre-fetched issues list.
	PickIssueFromTracker(ctx context.Context, issues []tracker.Issue, allowedTypes []string) (*Issue, error)

	// NotifyTrackerError shows a one-line error note when the tracker errors
	// out or returns no open issues. Returning a non-nil error aborts the flow.
	NotifyTrackerError(ctx context.Context, message string) error
}

type IssueStartFlags struct {
	TrackerFirst bool
	Variant      string
}

// Issue is the representation of a work item.
type Issue struct {
	Type string // feat, fix, doc, etc…
	tracker.Issue
}

// GetFromUser drives the manual issue-input flow via p.PickIssueFromUser.
func GetFromUser(ctx context.Context, p Prompter, allowedTypes []string) (*Issue, error) {
	out, err := p.PickIssueFromUser(ctx, allowedTypes)
	if err != nil {
		return nil, fmt.Errorf("issue input: %w", err)
	}

	return out, nil
}

// GetFromTracker fetches issues via t.ListIssues, then either falls back to
// the manual path (PickIssueFromUser) on error/empty-list, or drives the
// tracker picker (PickIssueFromTracker). All form opening is delegated to p.
func GetFromTracker(ctx context.Context, p Prompter, t tracker.Tracker, allowedTypes []string) (*Issue, error) {
	errMsg := ""
	issues, listErr := t.ListIssues(ctx)
	if listErr != nil {
		errMsg = listErr.Error()
	}

	if listErr == nil && len(issues) == 0 {
		errMsg = "no open issues assigned to you"
	}

	if errMsg != "" {
		if err := p.NotifyTrackerError(ctx, errMsg); err != nil {
			return nil, fmt.Errorf("notify tracker error: %w", err)
		}

		return GetFromUser(ctx, p, allowedTypes)
	}

	out, err := p.PickIssueFromTracker(ctx, issues, allowedTypes)
	if err != nil {
		return nil, fmt.Errorf("tracker picker: %w", err)
	}

	return out, nil
}
