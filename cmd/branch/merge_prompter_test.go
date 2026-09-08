package branch

import (
	"context"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/tui"
)

var _ MergePrompter = (*scriptedMergePrompter)(nil)

// scriptedMergePrompter returns canned answers instead of opening huh forms.
type scriptedMergePrompter struct {
	Source       SourceBranch
	Strategy     commit.MergeStrategy
	Confirm      bool
	Message      []byte
	DeleteSource bool

	PickSourceCalls    int
	PickStrategyCalls  int
	ConfirmDeleteCalls int
}

func (s *scriptedMergePrompter) PickSource(context.Context, []SourceBranch) (SourceBranch, error) {
	s.PickSourceCalls++

	return s.Source, nil
}

func (s *scriptedMergePrompter) PickStrategy(context.Context) (commit.MergeStrategy, error) {
	s.PickStrategyCalls++

	return s.Strategy, nil
}

func (s *scriptedMergePrompter) ConfirmMerge(context.Context, string, string, commit.MergeStrategy) (bool, error) {
	return s.Confirm, nil
}

func (s *scriptedMergePrompter) ComposeMessage(context.Context, map[string]any) ([]byte, tui.CommitOption, error) {
	return s.Message, tui.CommitOption{}, nil
}

func (s *scriptedMergePrompter) ConfirmDeleteSource(context.Context, string) (bool, error) {
	s.ConfirmDeleteCalls++

	return s.DeleteSource, nil
}
