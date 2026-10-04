package review

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

// ReviewPrompter resolves every user-facing decision in the review flow.
// The production implementation drives huh forms; tests use scriptedReviewPrompter.
type ReviewPrompter interface {
	// PickBranch presents a branch list with a configurable title and a smart
	// default (the branch whose IssueSlug matches currentSlug, or the first row).
	// Used by request, start, approve, reject, sync, and status subcommands.
	PickBranch(ctx context.Context, title string, branches []store.BranchRow, currentSlug string) (*store.BranchRow, error)

	// PickTrackerStatus presents the tracker's status list and returns the chosen
	// status name (or "" to skip). Used by request/approve/reject to update the
	// originating tracker, mirroring issue close.
	PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error)

	// Confirm presents a yes/no confirmation with the given title. Used by the
	// request flow to offer merging pending reviewer commits before re-requesting.
	Confirm(ctx context.Context, title string) (bool, error)

	// Text presents a free-form multi-line text field. An empty answer is
	// valid. Used by the reject flow to collect the reason for requesting changes.
	Text(ctx context.Context, title string) (string, error)
}

// Compile-time check.
var _ ReviewPrompter = (*huhReviewPrompter)(nil)

type huhReviewPrompter struct {
	issueflow.HuhPickers // PickTrackerStatus
}

func (p *huhReviewPrompter) PickBranch(ctx context.Context, title string, branches []store.BranchRow, currentSlug string) (*store.BranchRow, error) {
	var picked store.BranchRow
	onCurrent := func(b *store.BranchRow) bool { return b.IssueSlug == currentSlug }
	if err := huh.NewForm(tui.BranchPicker(title, branches, onCurrent, &picked)).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("branch picker: %w", err)
	}
	return &picked, nil
}

func (p *huhReviewPrompter) Confirm(ctx context.Context, title string) (bool, error) {
	confirmed := true
	form := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(title).Value(&confirmed)))
	if err := form.RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm form: %w", err)
	}
	return confirmed, nil
}

func (p *huhReviewPrompter) Text(ctx context.Context, title string) (string, error) {
	var text string
	form := huh.NewForm(huh.NewGroup(huh.NewText().Title(title).Value(&text)))
	if err := form.RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("text form: %w", err)
	}
	return text, nil
}
