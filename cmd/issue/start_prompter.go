package issue

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
)

// BranchClient is the subset of *git.Client that ResolveBranchConflict
// needs. Declared as a small interface so tests can substitute a fake without
// constructing a full *git.Client.
type BranchClient interface {
	BranchExists(name string) (bool, error)
	Checkout(ctx context.Context, branch string) error
	IO() *pkg.IO
}

// StartPrompter resolves every user-facing decision in the issue-start flow.
// The production implementation drives huh forms; the test implementation in
// start_prompter_test.go returns canned values.
//
// Each method maps 1:1 to a huh.NewForm call in the pre-refactor flow. The
// order below mirrors the order calls happen in RunIssueStart.
type StartPrompter interface {
	// Prompter contributes the three issue-input methods used by
	// issuepkg.GetFromUser and issuepkg.GetFromTracker.
	issuepkg.Prompter

	// PickUseTracker drives the "fetch from tracker?" toggle. Called only when
	// cfg.IssueTracker.Type != "". trackerFirst controls the pre-selected
	// option (true for `issue start`, false for `branch new`).
	PickUseTracker(ctx context.Context, trackerType string, trackerFirst bool) (use bool, err error)

	// PickUseWorktree drives the "worktree vs plain branch?" toggle. Called
	// only when cfg.Branch.UseWorktree == nil.
	PickUseWorktree(ctx context.Context) (use bool, err error)

	// ConfirmCreateBranch gates client.CreateBranch. message is the full
	// human-readable "Create branch %q based on %q?" string.
	ConfirmCreateBranch(ctx context.Context, message string) (confirmed bool, err error)

	// ConfirmCreateWorktree gates client.CreateWorktree. message is the full
	// "Create worktree %q at %q based on %q?" string.
	ConfirmCreateWorktree(ctx context.Context, message string) (confirmed bool, err error)

	// PickTrackerStatus drives the tracker status-picker form. An empty return
	// signals "no selection" — the caller decides whether to treat that as
	// skip or abort.
	PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error)

	// ResolveBranchConflict owns the conflict-resolution loop. It checks
	// whether b's name already exists locally and, if so, drives the operator
	// through a picker (checkout / variant / abort). Returns (b, nil) on the
	// clean no-collision path. Returns (nil, nil) to signal "stop here, do
	// not create or persist" (operator chose abort or checked out the
	// existing branch). Returns (nil, err) on input or git failures.
	ResolveBranchConflict(ctx context.Context, client BranchClient, b *branch.Branch, picked *issuepkg.Issue) (*branch.Branch, error)
}

// Compile-time check that *git.Client satisfies BranchClient (the
// production caller). Catches accidental signature drift on *git.Client.
var _ BranchClient = (*git.Client)(nil)

// Compile-time check.
var _ StartPrompter = (*huhStartPrompter)(nil)

// huhStartPrompter is the production StartPrompter. It opens real huh forms.
// Constructed once per `issue start` (or `branch new`) invocation.
type huhStartPrompter struct{}

// NewHuhStartPrompter constructs the production huh-driven StartPrompter.
// Exported so cmd/branch/branch.go can build one when delegating to
// RunIssueStart.
func NewHuhStartPrompter() *huhStartPrompter { //nolint:revive // returns unexported type by design — only the constructor needs to be public.
	return &huhStartPrompter{}
}

func (p *huhStartPrompter) PickIssueFromUser(ctx context.Context, allowedTypes []string) (*issuepkg.Issue, error) {
	var got issuepkg.Issue
	if err := huh.NewForm(
		tui.IssueInput(&got.ID, &got.Subject, &got.Type, allowedTypes),
	).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("issue form: %w", err)
	}

	return &got, nil
}

func (p *huhStartPrompter) PickIssueFromTracker(ctx context.Context, issues []tracker.Issue, allowedTypes []string) (*issuepkg.Issue, error) {
	var got issuepkg.Issue
	var pickedIssue tracker.Issue

	picker := tui.IssueTrackerPicker(issues, &pickedIssue, allowedTypes, &got.Type)
	if err := huh.NewForm(picker).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("tracker picker: %w", err)
	}

	got.ID = pickedIssue.ID
	got.Subject = pickedIssue.Subject
	got.TrackerType = pickedIssue.TrackerType

	return &got, nil
}

func (p *huhStartPrompter) NotifyTrackerError(ctx context.Context, message string) error {
	if err := huh.NewForm(tui.IssueTrackerError(message)).RunWithContext(ctx); err != nil {
		return fmt.Errorf("error note: %w", err)
	}

	return nil
}

func (p *huhStartPrompter) PickUseTracker(ctx context.Context, trackerType string, trackerFirst bool) (bool, error) {
	var use bool
	form := tui.IssueTrackerToggle(&use, trackerFirst, trackerType)
	if err := huh.NewForm(form).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("tracker toggle: %w", err)
	}

	return use, nil
}

func (p *huhStartPrompter) PickUseWorktree(ctx context.Context) (bool, error) {
	var use bool
	if err := huh.NewForm(tui.WorktreeToggle(&use)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("worktree toggle: %w", err)
	}

	return use, nil
}

func (p *huhStartPrompter) ConfirmCreateBranch(ctx context.Context, message string) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(message, &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm branch: %w", err)
	}

	return confirmed, nil
}

func (p *huhStartPrompter) ConfirmCreateWorktree(ctx context.Context, message string) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(message, &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm worktree: %w", err)
	}

	return confirmed, nil
}

func (p *huhStartPrompter) PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error) {
	var selected string
	if err := huh.NewForm(tui.IssueStatusPicker(issueID, trackerType, statuses, &selected)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("status picker form: %w", err)
	}

	return selected, nil
}

// ResolveBranchConflict is the production conflict-resolution loop. It is the
// body of the pre-refactor cmd/issue/conflict.go:resolveBranchConflict lifted
// verbatim onto the prompter so tests can substitute a scripted result.
func (p *huhStartPrompter) ResolveBranchConflict(ctx context.Context, client BranchClient, b *branch.Branch, picked *issuepkg.Issue) (*branch.Branch, error) {
	for {
		exists, err := client.BranchExists(b.Name())
		if err != nil {
			return nil, fmt.Errorf("check branch exists: %w", err)
		}

		if !exists {
			return b, nil
		}

		var action string
		if err := huh.NewForm(tui.BranchConflictPicker(b.Name(), &action)).RunWithContext(ctx); err != nil {
			return nil, fmt.Errorf("conflict picker: %w", err)
		}

		switch action {
		case "checkout":
			if err := client.Checkout(ctx, b.Name()); err != nil {
				return nil, fmt.Errorf("checkout existing: %w", err)
			}

			fmt.Fprintf(client.IO().Out, "Switched to existing branch %q\n", b.Name())

			return nil, nil
		case "abort":
			fmt.Fprintln(client.IO().Out, "Aborted.")

			return nil, nil
		case "variant":
			var label string
			if err := huh.NewForm(tui.VariantLabelInput(&label)).RunWithContext(ctx); err != nil {
				return nil, fmt.Errorf("variant input: %w", err)
			}

			newB, err := rebuildVariantBranch(picked, label)
			if err != nil {
				return nil, err
			}

			b = newB
		default:
			return nil, fmt.Errorf("unknown conflict action %q", action)
		}
	}
}
