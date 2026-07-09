package review

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

// ReviewPrompter resolves every user-facing decision in the review flow.
// The production implementation drives huh forms; tests use scriptedReviewPrompter.
type ReviewPrompter interface {
	// PickBranch presents a branch list with a configurable title and a smart
	// default (the branch whose IssueSlug matches currentSlug, or the first row).
	// Used by request, approve, reject, sync, and status subcommands.
	PickBranch(ctx context.Context, title string, branches []store.BranchRow, currentSlug string) (*store.BranchRow, error)

	// PickIssueToStart presents a list of issue slugs available to start reviewing.
	// Used by the start subcommand.
	PickIssueToStart(ctx context.Context, slugs []string) (string, error)

	// PickTrackerStatus presents the tracker's status list and returns the chosen
	// status name (or "" to skip). Used by request/approve/reject to update the
	// originating tracker, mirroring issue close.
	PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error)

	// Confirm presents a yes/no confirmation with the given title. Used by the
	// request flow to offer merging pending reviewer commits before re-requesting.
	Confirm(ctx context.Context, title string) (bool, error)
}

// Compile-time check.
var _ ReviewPrompter = (*huhReviewPrompter)(nil)

type huhReviewPrompter struct{}

func newHuhReviewPrompter() *huhReviewPrompter { return &huhReviewPrompter{} }

func (p *huhReviewPrompter) PickBranch(ctx context.Context, title string, branches []store.BranchRow, currentSlug string) (*store.BranchRow, error) {
	var picked store.BranchRow
	if err := huh.NewForm(tui.ReviewBranchPicker(title, branches, currentSlug, &picked)).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("branch picker: %w", err)
	}
	return &picked, nil
}

func (p *huhReviewPrompter) PickIssueToStart(ctx context.Context, slugs []string) (string, error) {
	var picked string
	if err := huh.NewForm(tui.ReviewIssueStartPicker(slugs, &picked)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("issue picker: %w", err)
	}
	return picked, nil
}

func (p *huhReviewPrompter) PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error) {
	var selected string
	if err := huh.NewForm(tui.IssueStatusPicker(issueID, trackerType, statuses, &selected)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("status picker form: %w", err)
	}
	return selected, nil
}

func (p *huhReviewPrompter) Confirm(ctx context.Context, title string) (bool, error) {
	confirmed := true
	form := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(title).Value(&confirmed)))
	if err := form.RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm form: %w", err)
	}
	return confirmed, nil
}

// scriptedReviewPrompter is the canned-response prompter used by review E2E tests.
type scriptedReviewPrompter struct {
	Branch           *store.BranchRow
	IssueSlug        string
	TrackerStatus    string
	BranchErr        error
	IssueErr         error
	TrackerStatusErr error
	ConfirmAnswer    bool
	ConfirmErr       error
}

var _ ReviewPrompter = (*scriptedReviewPrompter)(nil)

func (s *scriptedReviewPrompter) PickBranch(_ context.Context, _ string, _ []store.BranchRow, _ string) (*store.BranchRow, error) {
	if s.BranchErr != nil {
		return nil, s.BranchErr
	}
	return s.Branch, nil
}

func (s *scriptedReviewPrompter) PickIssueToStart(_ context.Context, _ []string) (string, error) {
	if s.IssueErr != nil {
		return "", s.IssueErr
	}
	return s.IssueSlug, nil
}

func (s *scriptedReviewPrompter) PickTrackerStatus(_ context.Context, _, _ string, _ []string) (string, error) {
	if s.TrackerStatusErr != nil {
		return "", s.TrackerStatusErr
	}
	return s.TrackerStatus, nil
}

func (s *scriptedReviewPrompter) Confirm(_ context.Context, _ string) (bool, error) {
	if s.ConfirmErr != nil {
		return false, s.ConfirmErr
	}
	return s.ConfirmAnswer, nil
}
