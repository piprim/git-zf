package mergeflow

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
)

var _ Prompter = (*HuhPrompter)(nil)

// HuhPrompter is the production Prompter, plus the post-merge confirms. The
// `issue close` and `branch merge` prompters embed it and add their own
// pickers, so both flows present an identical merge UX.
type HuhPrompter struct {
	Client *git.Client       // Authors() for the commit-message form
	Cfg    *config.AppConfig // template of the commit-message form

	// TargetLabel names the merge target in the rebase hint ("local base",
	// "current").
	TargetLabel string
}

func (p *HuhPrompter) PickStrategy(ctx context.Context) (commit.MergeStrategy, error) {
	var picked string
	form := tui.IssueMergeStrategy(&picked, []tui.StrategyOption{
		{
			Value: string(commit.MergeStrategyRebase),
			Label: "Rebase",
			Hint:  "Single clean commit on " + p.TargetLabel + ", submodule-safe (recommended)",
		},
		{
			Value: string(commit.MergeStrategySquash),
			Label: "Squash",
			Hint:  "git merge --squash — fast, but not submodule-safe",
		},
		{
			Value: string(commit.MergeStrategyClassic),
			Label: "Classic",
			Hint:  "git merge --no-ff with commitizen message — preserves full history",
		},
	})
	if err := huh.NewForm(form).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("strategy picker: %w", err)
	}

	return commit.MergeStrategy(picked), nil
}

func (*HuhPrompter) ConfirmMerge(ctx context.Context, source, target string, s commit.MergeStrategy) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueMergeConfirm(source, target, string(s), &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm form: %w", err)
	}

	return confirmed, nil
}

func (p *HuhPrompter) ComposeMessage(ctx context.Context, prefill map[string]any) ([]byte, tui.CommitOption, error) {
	authors, err := p.Client.Authors(ctx)
	if err != nil {
		slog.Warn("could not load author list", "error", err)

		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	history, err := commit.OpenHistory(p.Client)
	if err != nil {
		return nil, tui.CommitOption{}, fmt.Errorf("open commit history: %w", err)
	}

	msg, opts, err := commit.FillOutForm(ctx, p.Cfg, defaults, history, prefill, nil)
	if err != nil {
		return nil, tui.CommitOption{}, fmt.Errorf("fill commit form: %w", err)
	}

	return msg, opts, nil
}

// ConfirmRemoveWorktree runs after a successful merge, only when the merged
// branch was checked out in a linked worktree, and before ConfirmDeleteBranch.
func (*HuhPrompter) ConfirmRemoveWorktree(ctx context.Context, path string) (bool, error) {
	var remove bool
	if err := huh.NewForm(tui.IssueRemoveWorktree(path, &remove)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("remove worktree form: %w", err)
	}

	return remove, nil
}

// ConfirmDeleteBranch runs after a successful merge.
func (*HuhPrompter) ConfirmDeleteBranch(ctx context.Context, branchName string) (bool, error) {
	var shouldDelete bool
	if err := huh.NewForm(tui.IssueDeleteBranch(branchName, &shouldDelete)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("delete branch form: %w", err)
	}

	return shouldDelete, nil
}
