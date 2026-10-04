package branch

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/cmd/mergeflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// SourceBranch is one pickable merge source. RemoteOnly marks a branch that
// exists on origin but not locally, so the picker can label it and runMerge
// knows to materialize it before merging.
type SourceBranch struct {
	Name       string
	RemoteOnly bool
}

// MergePrompter resolves every user-facing decision in the branch-merge flow.
// huhMergePrompter drives huh forms; merge_prompter_test.go provides a scripted
// implementation for the E2E tests.
type MergePrompter interface {
	mergeflow.Prompter // PickStrategy, ConfirmMerge, ComposeMessage
	PickSource(ctx context.Context, sources []SourceBranch) (SourceBranch, error)
	// ConfirmRemoveWorktree runs after a successful merge, only when the
	// source was checked out in a linked worktree.
	ConfirmRemoveWorktree(ctx context.Context, path string) (remove bool, err error)
	// ConfirmDeleteBranch runs after a successful merge, on the source branch.
	ConfirmDeleteBranch(ctx context.Context, source string) (del bool, err error)
}

var _ MergePrompter = (*huhMergePrompter)(nil)

// huhMergePrompter is the production MergePrompter: the shared merge forms
// plus the source picker.
type huhMergePrompter struct {
	mergeflow.HuhPrompter
}

func newHuhMergePrompter(c *git.Client, s *store.Store, cfg *config.AppConfig) *huhMergePrompter {
	return &huhMergePrompter{mergeflow.HuhPrompter{Client: c, Store: s, Cfg: cfg, TargetLabel: "current"}}
}

func (p *huhMergePrompter) PickSource(ctx context.Context, sources []SourceBranch) (SourceBranch, error) {
	opts := make([]huh.Option[string], 0, len(sources))
	byName := make(map[string]SourceBranch, len(sources))
	for _, s := range sources {
		label := s.Name
		if s.RemoteOnly {
			label = s.Name + " (origin)"
		}
		opts = append(opts, huh.NewOption(label, s.Name))
		byName[s.Name] = s
	}

	var picked string
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Branch to merge into current:").
			Options(opts...).
			Value(&picked),
	))
	if err := form.RunWithContext(ctx); err != nil {
		return SourceBranch{}, fmt.Errorf("source picker: %w", err)
	}

	return byName[picked], nil
}
