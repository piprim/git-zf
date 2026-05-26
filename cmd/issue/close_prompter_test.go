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

	// Failure injection — when non-nil, that method returns this error
	// immediately. Useful for cancellation-style tests.
	BranchErr        error
	StrategyErr      error
	ConfirmErr       error
	MessageErr       error
	TrackerStatusErr error
	DeleteBranchErr  error
}

func (s *scriptedPrompter) PickBranch(_ context.Context, _ []store.BranchRow, _ string) (*store.BranchRow, error) {
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

func (s *scriptedPrompter) ComposeMessage(_ context.Context, _ map[string]any) ([]byte, tui.CommitOption, error) {
	if s.MessageErr != nil {
		return nil, tui.CommitOption{}, s.MessageErr
	}

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
