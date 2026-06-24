package issue

import (
	"context"

	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

// Compile-time check: scriptedPrompter must satisfy ClosePrompter.
var _ ClosePrompter = (*scriptedPrompter)(nil)

// scriptedPrompter is the canned-response prompter used by close_e2e_test.go.
// Each field corresponds to one ClosePrompter method's return value.
//
// Unset fields produce zero values — useful for tests that exercise a single
// strategy and don't care about the rest of the surface. Fields are public to
// keep test setup readable (literal struct construction).
type scriptedPrompter struct {
	Branch        *store.BranchRow
	Strategy      MergeStrategy
	Confirm       bool
	Message       []byte
	MessageOpts   tui.CommitOption
	TrackerStatus string
	DeleteBranch  bool
	Base          string
	BaseErr       error
	BaseCalled    bool

	// Failure injection — when non-nil, that method returns this error
	// immediately. Useful for cancellation-style tests.
	BranchErr        error
	StrategyErr      error
	ConfirmErr       error
	MessageErr       error
	TrackerStatusErr error
	DeleteBranchErr  error

	// CapturedPrefill holds the last prefill map received by ComposeMessage.
	// Tests can assert on it after runClose returns.
	CapturedPrefill map[string]any

	// PickBranchSeen holds the branch list PickBranch was last offered. Tests
	// assert on it to verify which branches the picker would have shown.
	PickBranchSeen []store.BranchRow
}

func (s *scriptedPrompter) PickBranch(_ context.Context, branches []store.BranchRow, _ string) (*store.BranchRow, error) {
	s.PickBranchSeen = branches

	if s.BranchErr != nil {
		return nil, s.BranchErr
	}

	return s.Branch, nil
}

func (s *scriptedPrompter) PickStrategy(_ context.Context) (MergeStrategy, error) {
	if s.StrategyErr != nil {
		return "", s.StrategyErr
	}

	return s.Strategy, nil
}

func (s *scriptedPrompter) ConfirmMerge(_ context.Context, _, _ string, _ MergeStrategy) (bool, error) {
	if s.ConfirmErr != nil {
		return false, s.ConfirmErr
	}

	return s.Confirm, nil
}

func (s *scriptedPrompter) ComposeMessage(_ context.Context, prefill map[string]any) ([]byte, tui.CommitOption, error) {
	if s.MessageErr != nil {
		return nil, tui.CommitOption{}, s.MessageErr
	}

	s.CapturedPrefill = prefill

	return s.Message, s.MessageOpts, nil
}

func (s *scriptedPrompter) PickTrackerStatus(_ context.Context, _, _ string, _ []string) (string, error) {
	if s.TrackerStatusErr != nil {
		return "", s.TrackerStatusErr
	}

	return s.TrackerStatus, nil
}

func (s *scriptedPrompter) ConfirmDeleteBranch(_ context.Context, _ string) (bool, error) {
	if s.DeleteBranchErr != nil {
		return false, s.DeleteBranchErr
	}

	return s.DeleteBranch, nil
}

func (s *scriptedPrompter) PickBaseBranch(_ context.Context, defaultBase string, _ []string) (string, error) {
	s.BaseCalled = true
	if s.BaseErr != nil {
		return "", s.BaseErr
	}
	if s.Base == "" {
		return defaultBase, nil
	}

	return s.Base, nil
}
