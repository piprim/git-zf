package issue

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
)

type IssueStartFlags struct {
	TrackerFirst bool
}

// Issue is the representation of a work item.
type Issue struct {
	Type string // feat, fix, doc, etc…
	tracker.Issue
}

func GetFromUser(allowedTypes []string) (*Issue, error) {
	var issue = Issue{}

	if err := huh.NewForm(tui.IssueInput(&issue.ID, &issue.Subject, &issue.Type, allowedTypes)).Run(); err != nil {
		return nil, fmt.Errorf("issue form: %w", err)
	}

	return &issue, nil
}

func GetFromTracker(ctx context.Context, t tracker.Tracker, allowedTypes []string) (*Issue, error) {
	var issue = Issue{}

	errMsg := ""
	var pickedIssue tracker.Issue
	issues, listErr := t.ListIssues(ctx)
	if listErr != nil {
		errMsg = listErr.Error()
	}
	if listErr == nil && len(issues) == 0 {
		errMsg = "no open issues assigned to you"
	}

	if errMsg != "" {
		if err := huh.NewForm(tui.IssueTrackerError(errMsg)).Run(); err != nil {
			return nil, fmt.Errorf("error note: %w", err)
		}

		return GetFromUser(allowedTypes)
	}

	issueTrackerPicker := tui.IssueTrackerPicker(issues, &pickedIssue, allowedTypes, &issue.Type)
	if err := huh.NewForm(issueTrackerPicker).Run(); err != nil {
		return nil, fmt.Errorf("tracker picker: %w", err)
	}

	issue.ID = pickedIssue.ID
	issue.Subject = pickedIssue.Subject
	issue.TrackerType = pickedIssue.TrackerType

	return &issue, nil
}
