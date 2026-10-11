package issue

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// Compile-time check: scriptedStartPrompter must satisfy StartPrompter.
var _ issueflow.StartPrompter = (*scriptedStartPrompter)(nil)

// scriptedStartPrompter is the canned-response prompter used by
// start_e2e_test.go. Each field corresponds to one StartPrompter method's
// return value; *Err fields inject errors.
type scriptedStartPrompter struct {
	// Issue-input return values.
	IssueFromUser    *issuepkg.Issue
	IssueFromTracker *issuepkg.Issue

	// PickRow answers: PickSlug names the row to pick among those offered;
	// PickNew picks "new issue" instead (nil, nil); PickRowErr fails the
	// pick. A PickSlug that is not offered is an error: the test scripted
	// a row the flow did not list.
	PickSlug   string
	PickNew    bool
	PickRowErr error
	// OfferedRows records what PickRow was offered; nil ⇒ the table was
	// never opened (RunIssueStart skips it when there are no rows).
	OfferedRows []issuepkg.Row

	// Toggle return value.
	UseWorktree bool

	// Confirm return values.
	ConfirmBranch   bool
	ConfirmWorktree bool

	// Tracker status picker return value (empty = skip).
	TrackerStatus string

	// ResolveBranchConflict return — set Branch to the (possibly variant)
	// *branch.Branch to proceed, or leave nil + ConflictAbort=true to signal
	// "stop, no error" (operator aborted or checked out existing).
	ConflictBranch *branch.Branch
	ConflictAbort  bool

	// BaseBranch picker return value and error injection.
	BaseBranch    string
	BaseBranchErr error

	// CapturedBaseBranches records the candidate list PickBaseBranch was offered,
	// so tests can assert a remote-only parent was presented. nil ⇒ the picker
	// was never invoked (gated off by candidate count).
	CapturedBaseBranches []string

	// Error injection — when non-nil, the corresponding method returns this
	// error immediately.
	IssueFromUserErr    error
	IssueFromTrackerErr error
	UseWorktreeErr      error
	ConfirmBranchErr    error
	ConfirmWorktreeErr  error
	TrackerStatusErr    error
	ConflictErr         error
}

func (s *scriptedStartPrompter) PickIssueFromUser(_ context.Context, _ []string) (*issuepkg.Issue, error) {
	if s.IssueFromUserErr != nil {
		return nil, s.IssueFromUserErr
	}

	return s.IssueFromUser, nil
}

func (s *scriptedStartPrompter) PickIssueFromTracker(_ context.Context, _ []tracker.Issue, _ []string) (*issuepkg.Issue, error) {
	if s.IssueFromTrackerErr != nil {
		return nil, s.IssueFromTrackerErr
	}

	return s.IssueFromTracker, nil
}

func (s *scriptedStartPrompter) PickRow(_ context.Context, rows []issuepkg.Row) (*issuepkg.Row, error) {
	s.OfferedRows = rows
	if s.PickRowErr != nil {
		return nil, s.PickRowErr
	}
	if s.PickNew {
		return nil, nil
	}
	for i := range rows {
		if rows[i].IssueSlug == s.PickSlug {
			return &rows[i], nil
		}
	}

	return nil, fmt.Errorf("scripted PickRow: no row %q among the %d offered", s.PickSlug, len(rows))
}

func (s *scriptedStartPrompter) PickUseWorktree(_ context.Context) (bool, error) {
	if s.UseWorktreeErr != nil {
		return false, s.UseWorktreeErr
	}

	return s.UseWorktree, nil
}

func (s *scriptedStartPrompter) PickBaseBranch(_ context.Context, _ string, branches []string) (string, error) {
	s.CapturedBaseBranches = branches
	return s.BaseBranch, s.BaseBranchErr
}

func (s *scriptedStartPrompter) ConfirmCreateBranch(_ context.Context, _ string) (bool, error) {
	if s.ConfirmBranchErr != nil {
		return false, s.ConfirmBranchErr
	}

	return s.ConfirmBranch, nil
}

func (s *scriptedStartPrompter) ConfirmCreateWorktree(_ context.Context, _ string) (bool, error) {
	if s.ConfirmWorktreeErr != nil {
		return false, s.ConfirmWorktreeErr
	}

	return s.ConfirmWorktree, nil
}

func (s *scriptedStartPrompter) PickTrackerStatus(_ context.Context, _, _ string, _ []string) (string, error) {
	if s.TrackerStatusErr != nil {
		return "", s.TrackerStatusErr
	}

	return s.TrackerStatus, nil
}

// ResolveBranchConflict applies the canned outcome in this order:
//
//	ConflictErr     → return (nil, err)
//	ConflictAbort   → return (nil, nil)
//	ConflictBranch  → return (ConflictBranch, nil)
//	default         → return the branch passed in, unchanged (no-collision path)
func (s *scriptedStartPrompter) ResolveBranchConflict(_ context.Context, _ *git.Client, b *branch.Branch, _ *issuepkg.Issue) (*branch.Branch, error) {
	if s.ConflictErr != nil {
		return nil, s.ConflictErr
	}

	if s.ConflictAbort {
		return nil, nil
	}

	if s.ConflictBranch != nil {
		return s.ConflictBranch, nil
	}

	return b, nil
}
