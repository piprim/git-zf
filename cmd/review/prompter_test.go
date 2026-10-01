package review

import (
	"context"

	"github.com/piprim/git-zf/store"
)

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
	TextAnswer       string
	TextErr          error
	TextCalls        int
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

func (s *scriptedReviewPrompter) Text(_ context.Context, _ string) (string, error) {
	s.TextCalls++
	if s.TextErr != nil {
		return "", s.TextErr
	}
	return s.TextAnswer, nil
}
