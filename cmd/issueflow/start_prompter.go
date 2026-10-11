package issueflow

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
)

// StartPrompter resolves every user-facing decision in the issue-start flow.
// The production implementation drives the issue table and huh forms; the
// test implementation in cmd/issue's start_prompter_test.go returns canned
// values. The order below mirrors the order calls happen in RunIssueStart.
type StartPrompter interface {
	// PickRow opens the issue table over rows. A nil row with a nil error
	// means "new issue": the flow falls through to PickIssueFromUser.
	PickRow(ctx context.Context, rows []issuepkg.Row) (*issuepkg.Row, error)

	// PickIssueFromTracker asks the branch type of a tracker issue: the
	// form lists issues (one, from now on) and the types.
	PickIssueFromTracker(ctx context.Context, issues []tracker.Issue, allowedTypes []string) (*issuepkg.Issue, error)

	// PickIssueFromUser opens the manual issue-input form (issue ID, subject, type).
	PickIssueFromUser(ctx context.Context, allowedTypes []string) (*issuepkg.Issue, error)

	// PickUseWorktree drives the "worktree vs plain branch?" toggle. Called
	// only when cfg.Branch.UseWorktree == nil.
	PickUseWorktree(ctx context.Context) (use bool, err error)

	// PickBaseBranch presents a picker over all local git branches.
	// defaultBase is pre-selected. Called only when --parent was not explicitly
	// provided and the repo has more than one local branch.
	PickBaseBranch(ctx context.Context, defaultBase string, branches []string) (baseBranch string, err error)

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
	ResolveBranchConflict(
		ctx context.Context, client *git.Client, b *branch.Branch, picked *issuepkg.Issue,
	) (*branch.Branch, error)
}

// Compile-time check.
var _ StartPrompter = (*HuhStartPrompter)(nil)

// HuhStartPrompter is the production StartPrompter. It opens real huh forms.
// Constructed once per `issue start` (or `branch new`) invocation.
type HuhStartPrompter struct{ HuhPickers }

// HuhPickers holds the huh forms the start, close and review prompters share.
// Each of them embeds it.
type HuhPickers struct{}

// PickTrackerStatus presents the tracker's status list and returns the chosen
// status name, or "" when the operator skips.
func (HuhPickers) PickTrackerStatus(
	ctx context.Context, issueID, trackerType string, statuses []string,
) (string, error) {
	var selected string
	if err := huh.NewForm(
		tui.IssueStatusPicker(issueID, trackerType, statuses, &selected)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("status picker form: %w", err)
	}

	return selected, nil
}

// PickBaseBranch lets the operator choose a base branch among branches, with
// defaultBase pre-selected.
func (HuhPickers) PickBaseBranch(ctx context.Context, defaultBase string, branches []string) (string, error) {
	var picked string
	if err := huh.NewForm(tui.BaseBranchPicker(defaultBase, branches, &picked)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("base branch picker: %w", err)
	}

	return picked, nil
}

// NewHuhStartPrompter constructs the production huh-driven StartPrompter.
func NewHuhStartPrompter() *HuhStartPrompter {
	return &HuhStartPrompter{}
}

func (p *HuhStartPrompter) PickIssueFromUser(ctx context.Context, allowedTypes []string) (*issuepkg.Issue, error) {
	var got issuepkg.Issue
	if err := huh.NewForm(
		tui.IssueInput(&got.ID, &got.Subject, &got.Type, allowedTypes),
	).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("issue form: %w", err)
	}

	return &got, nil
}

func (p *HuhStartPrompter) PickIssueFromTracker(ctx context.Context, issues []tracker.Issue, allowedTypes []string) (*issuepkg.Issue, error) {
	var got issuepkg.Issue
	idx := -1

	picker := tui.IssueTrackerPicker(issues, &idx, allowedTypes, &got.Type)
	if err := huh.NewForm(picker).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("tracker picker: %w", err)
	}

	var pickedIssue tracker.Issue
	if idx >= 0 {
		pickedIssue = issues[idx]
	}

	got.ID = pickedIssue.ID
	got.Subject = pickedIssue.Subject
	got.TrackerType = pickedIssue.TrackerType

	return &got, nil
}

// PickRow opens the issue table over rows: enter or s picks the row under
// the cursor, n asks for a new issue, q gives up (huh.ErrUserAborted, as the
// huh picker's Esc was).
func (*HuhStartPrompter) PickRow(ctx context.Context, rows []issuepkg.Row) (*issuepkg.Row, error) {
	m, err := tui.IssueTableModel(rows, issuepkg.StateOpen, tui.IssueActions{Pick: true, NewIssue: true, Start: true})
	if err != nil {
		return nil, fmt.Errorf("issue table: %w", err)
	}
	final, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if err != nil {
		return nil, fmt.Errorf("issue table: %w", err)
	}

	pick, slug := tui.PickedIssue(final)
	switch pick {
	case tui.IssuePickNew:
		return nil, nil
	case tui.IssuePickRow:
		for i := range rows {
			if rows[i].IssueSlug == slug {
				return &rows[i], nil
			}
		}
	case tui.IssuePickNone:
	}

	return nil, huh.ErrUserAborted
}

func (p *HuhStartPrompter) PickUseWorktree(ctx context.Context) (bool, error) {
	var use bool
	if err := huh.NewForm(tui.WorktreeToggle(&use)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("worktree toggle: %w", err)
	}

	return use, nil
}

func (p *HuhStartPrompter) ConfirmCreateBranch(ctx context.Context, message string) (bool, error) {
	var confirmed = true
	if err := huh.NewForm(tui.IssueConfirm(message, &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm branch: %w", err)
	}

	return confirmed, nil
}

func (p *HuhStartPrompter) ConfirmCreateWorktree(ctx context.Context, message string) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(message, &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm worktree: %w", err)
	}

	return confirmed, nil
}

// ResolveBranchConflict is the production conflict-resolution loop. It is the
// body of the pre-refactor cmd/issue/conflict.go:resolveBranchConflict lifted
// verbatim onto the prompter so tests can substitute a scripted result.
func (*HuhStartPrompter) ResolveBranchConflict(
	ctx context.Context, client *git.Client, b *branch.Branch, picked *issuepkg.Issue,
) (*branch.Branch, error) {
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
