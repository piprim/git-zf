package issue

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/commit"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

// ClosePrompter resolves every user-facing decision in the close flow. The
// production implementation drives huh forms; the test implementation in
// close_prompter_test.go returns canned values.
//
// Each method maps 1:1 to a huh.NewForm call in the pre-refactor close.go.
// The order below mirrors the order calls happen in runClose.
type ClosePrompter interface {
	// PickBranch is called only when at least one in-progress branch exists.
	// A non-nil error indicates cancellation or an internal failure.
	PickBranch(ctx context.Context, branches []store.BranchRow, current string) (*store.BranchRow, error)

	// PickStrategy is called only when MergeDryRun reports no conflicts.
	PickStrategy(ctx context.Context) (commit.MergeStrategy, error)

	// ConfirmMerge gates the actual merge. confirmed=false means the operator
	// declined; runClose prints "Aborted." and returns nil.
	ConfirmMerge(ctx context.Context, branch, base string, strategy commit.MergeStrategy) (confirmed bool, err error)

	// ComposeMessage runs inline AFTER the strategy has staged its changes
	// (after MergeSquash / MergeRebase+soft-reset / MergeNoFFNoCommit). The
	// prefill is already populated with issue-derived type/scope and a
	// strategy-specific subject. The returned tui.CommitOption is passed to
	// git.Client.Commit verbatim.
	ComposeMessage(ctx context.Context, prefill map[string]any) ([]byte, tui.CommitOption, error)

	// PickTrackerStatus is called only when a tracker is configured AND the
	// merge succeeded. An empty return signals "no selection" — the caller
	// decides whether to treat that as skip or abort.
	PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error)

	// ConfirmDeleteBranch runs after a successful merge.
	ConfirmDeleteBranch(ctx context.Context, branchName string) (delete bool, err error)

	// PickBaseBranch lets the operator choose the merge target. It is called
	// only when no --base override was given AND more than one candidate branch
	// exists. defaultBase is pre-selected; branches is the candidate list.
	PickBaseBranch(ctx context.Context, defaultBase string, branches []string) (string, error)
}

// Compile-time check.
var _ ClosePrompter = (*huhPrompter)(nil)

// huhPrompter is the production ClosePrompter. It is constructed once per
// `issue close` invocation and holds the dependencies needed to drive the
// commit-message form (client for Authors(), store as the history backend,
// cfg for the form template).
type huhPrompter struct {
	client *git.Client
	store  *store.Store
	cfg    *config.AppConfig
}

func newHuhPrompter(client *git.Client, s *store.Store, cfg *config.AppConfig) *huhPrompter {
	return &huhPrompter{client: client, store: s, cfg: cfg}
}

func (p *huhPrompter) PickBranch(ctx context.Context, branches []store.BranchRow, current string) (*store.BranchRow, error) {
	var picked store.BranchRow
	if err := huh.NewForm(tui.IssueBranchPicker(branches, current, &picked)).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("branch picker: %w", err)
	}

	return &picked, nil
}

func (p *huhPrompter) PickStrategy(ctx context.Context) (commit.MergeStrategy, error) {
	var picked string
	form := tui.IssueMergeStrategy(&picked, []tui.StrategyOption{
		{
			Value: string(commit.MergeStrategyRebase),
			Label: "Rebase",
			Hint:  "Single clean commit on local base, submodule-safe (recommended)",
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

func (p *huhPrompter) ConfirmMerge(ctx context.Context, branch, base string, strategy commit.MergeStrategy) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueMergeConfirm(branch, base, string(strategy), &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm form: %w", err)
	}

	return confirmed, nil
}

func (p *huhPrompter) ComposeMessage(ctx context.Context, prefill map[string]any) ([]byte, tui.CommitOption, error) {
	authors, err := p.client.Authors(ctx)
	if err != nil {
		slog.Warn("could not load author list", "error", err)

		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	msg, opts, err := commitpkg.FillOutForm(ctx, p.cfg, defaults, p.store, prefill, "")
	if err != nil {
		return nil, tui.CommitOption{}, fmt.Errorf("fill commit form: %w", err)
	}

	return msg, opts, nil
}

func (p *huhPrompter) PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error) {
	var selected string
	if err := huh.NewForm(tui.IssueStatusPicker(issueID, trackerType, statuses, &selected)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("status picker form: %w", err)
	}

	return selected, nil
}

func (p *huhPrompter) ConfirmDeleteBranch(ctx context.Context, branchName string) (bool, error) {
	var shouldDelete bool
	if err := huh.NewForm(tui.IssueDeleteBranch(branchName, &shouldDelete)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("delete branch form: %w", err)
	}

	return shouldDelete, nil
}

func (p *huhPrompter) PickBaseBranch(ctx context.Context, defaultBase string, branches []string) (string, error) {
	var picked string
	if err := huh.NewForm(tui.BaseBranchPicker(defaultBase, branches, &picked)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("base branch picker: %w", err)
	}

	return picked, nil
}
