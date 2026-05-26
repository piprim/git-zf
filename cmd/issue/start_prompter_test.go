package issue

import (
	"context"

	"github.com/piprim/git-zf/branch"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// Compile-time check: scriptedStartPrompter must satisfy StartPrompter.
var _ StartPrompter = (*scriptedStartPrompter)(nil)

// scriptedStartPrompter is the canned-response prompter used by
// start_e2e_test.go. Each field corresponds to one StartPrompter method's
// return value; *Err fields inject errors.
type scriptedStartPrompter struct {
	// Issue-input return values.
	IssueFromUser    *issuepkg.Issue
	IssueFromTracker *issuepkg.Issue

	// Toggle return values.
	UseTracker  bool
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

	// Counter set by NotifyTrackerError so tests can assert "fallback fired".
	TrackerErrorNotifications int

	// Error injection — when non-nil, the corresponding method returns this
	// error immediately.
	IssueFromUserErr    error
	IssueFromTrackerErr error
	TrackerErrorErr     error
	UseTrackerErr       error
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

func (s *scriptedStartPrompter) NotifyTrackerError(_ context.Context, _ string) error {
	s.TrackerErrorNotifications++

	return s.TrackerErrorErr
}

func (s *scriptedStartPrompter) PickUseTracker(_ context.Context, _ string, _ bool) (bool, error) {
	if s.UseTrackerErr != nil {
		return false, s.UseTrackerErr
	}

	return s.UseTracker, nil
}

func (s *scriptedStartPrompter) PickUseWorktree(_ context.Context) (bool, error) {
	if s.UseWorktreeErr != nil {
		return false, s.UseWorktreeErr
	}

	return s.UseWorktree, nil
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
func (s *scriptedStartPrompter) ResolveBranchConflict(_ context.Context, _ BranchClient, b *branch.Branch, _ *issuepkg.Issue) (*branch.Branch, error) {
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
