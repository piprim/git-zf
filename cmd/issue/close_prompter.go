package issue

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/mergeflow"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
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
	PickBranch(ctx context.Context, branches []branch.Row, current string) (*branch.Row, error)

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

	// ConfirmRemoveWorktree runs after a successful merge, only when the
	// branch was checked out in a linked worktree, and before
	// ConfirmDeleteBranch.
	ConfirmRemoveWorktree(ctx context.Context, path string) (remove bool, err error)

	// PickBaseBranch lets the operator choose the merge target. It is called
	// only when no --base override was given AND more than one candidate branch
	// exists. defaultBase is pre-selected; branches is the candidate list.
	PickBaseBranch(ctx context.Context, defaultBase string, branches []string) (string, error)
}

// Compile-time check.
var _ ClosePrompter = (*huhPrompter)(nil)

// The generic subset of ClosePrompter also drives the shared merge engine, so
// the same production prompter satisfies mergeflow.Prompter.
var _ mergeflow.Prompter = (*huhPrompter)(nil)

// huhPrompter is the production ClosePrompter: the shared merge forms plus the
// close-only pickers. It is constructed once per `issue close` invocation.
type huhPrompter struct {
	mergeflow.HuhPrompter
	issueflow.HuhPickers // PickTrackerStatus, PickBaseBranch
}

func newHuhPrompter(client *git.Client, cfg *config.AppConfig) *huhPrompter {
	return &huhPrompter{
		HuhPrompter: mergeflow.HuhPrompter{Client: client, Cfg: cfg, TargetLabel: "local base"},
	}
}

func (p *huhPrompter) PickBranch(ctx context.Context, branches []branch.Row, current string) (*branch.Row, error) {
	var picked branch.Row
	onCurrent := func(b *branch.Row) bool { return b.BranchName == current }
	if err := huh.NewForm(
		tui.BranchPicker("Select branch to close:", branches, onCurrent, &picked)).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("branch picker: %w", err)
	}

	return &picked, nil
}
