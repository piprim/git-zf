package review

import (
	"context"

	"github.com/piprim/git-zf/branch"
)

// scriptedReviewPrompter is the canned-response prompter used by review E2E tests.
type scriptedReviewPrompter struct {
	Branch           *branch.Row
	Offered          []branch.Row // the rows the last PickBranch was shown
	TrackerStatus    string
	BranchErr        error
	TrackerStatusErr error
	TextAnswer       string
	TextErr          error
	TextCalls        int
}

var _ ReviewPrompter = (*scriptedReviewPrompter)(nil)

func (s *scriptedReviewPrompter) PickBranch(_ context.Context, _ string, rows []branch.Row, _ string) (*branch.Row, error) {
	s.Offered = rows
	if s.BranchErr != nil {
		return nil, s.BranchErr
	}
	return s.Branch, nil
}

func (s *scriptedReviewPrompter) PickTrackerStatus(_ context.Context, _, _ string, _ []string) (string, error) {
	if s.TrackerStatusErr != nil {
		return "", s.TrackerStatusErr
	}
	return s.TrackerStatus, nil
}

func (s *scriptedReviewPrompter) Text(_ context.Context, _ string) (string, error) {
	s.TextCalls++
	if s.TextErr != nil {
		return "", s.TextErr
	}
	return s.TextAnswer, nil
}
