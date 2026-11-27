package issue

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/mergeflow"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
	"golang.org/x/term"
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

	// ConfirmTakeRemote is called when the remote branch holds commits the
	// local one lacks. It returns ErrCannotAsk when there is no terminal to
	// ask on, which runClose treats as a no. Whatever the answer, the close
	// stops after it.
	ConfirmTakeRemote(ctx context.Context, offer RemoteOffer) (take bool, err error)
}

// RemoteOffer is what the close asks before taking the remote's commits.
type RemoteOffer struct {
	Branch, RemoteRef   string
	LocalTip, RemoteTip string // short SHAs
	Commits             int    // commits the remote holds and the local branch lacks
	Merge               bool   // true: a merge (diverged), default answer no; false: a fast-forward
}

// ErrCannotAsk is returned by ConfirmTakeRemote when stdin is not a terminal.
var ErrCannotAsk = errors.New("no terminal to ask on")

// ErrRemoteUnreachable is returned by runClose when the fetch failed: what
// the remote branch holds is unknown, and a close would merge without it.
var ErrRemoteUnreachable = errors.New("the remote did not answer, the issue is not closed")

// ErrRemoteMoved is returned by runClose when it stopped at the remote check.
var ErrRemoteMoved = errors.New("the remote branch moved, the issue is not closed")

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
		HuhPrompter: mergeflow.HuhPrompter{Client: client, Cfg: cfg},
	}
}

func (p *huhPrompter) ConfirmTakeRemote(ctx context.Context, o RemoteOffer) (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, ErrCannotAsk
	}

	title := fmt.Sprintf("Fast-forward %q to %s?", o.Branch, o.RemoteRef)
	desc := fmt.Sprintf("%s holds %d commit(s) your branch lacks (yours: %s, remote: %s).\n"+
		"The close stops after this, for you to look at what came in.", o.RemoteRef, o.Commits, o.LocalTip, o.RemoteTip)
	// A merge is right for work done in two places, wrong for a branch
	// rewritten on the remote: git cannot tell, so the default is no.
	take := !o.Merge
	if o.Merge {
		title = fmt.Sprintf("Merge %s into %q?", o.RemoteRef, o.Branch)
		desc = fmt.Sprintf("Your branch (%s) and %s (%s) have diverged: the remote holds %d commit(s) yours lacks.\n"+
			"If the remote branch was rebased, a merge brings back what the rebase dropped: say no and reset instead.\n"+
			"The close stops after this, for the merge to be looked at.", o.LocalTip, o.RemoteRef, o.RemoteTip, o.Commits)
	}

	group := huh.NewGroup(huh.NewConfirm().Title(title).Description(desc).Value(&take))
	if err := tui.NewForm(group).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm form: %w", err)
	}

	return take, nil
}

func (p *huhPrompter) PickBranch(ctx context.Context, branches []branch.Row, current string) (*branch.Row, error) {
	var picked branch.Row
	onCurrent := func(b *branch.Row) bool { return b.BranchName == current }
	if err := tui.NewForm(
		tui.BranchPicker("Select branch to close:", branches, onCurrent, &picked)).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("branch picker: %w", err)
	}

	return &picked, nil
}
