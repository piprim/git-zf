package issue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	reviewpkg "github.com/piprim/git-zf/review"
)

// shaLen is how much of a SHA a message shows.
const shaLen = 7

func shortSHA(sha string) string { return sha[:min(len(sha), shaLen)] }

// checkRemoteBranch is the remote branch check of a close. nil means the
// remote branch holds nothing the local one lacks (or there is none) and the
// close goes on. Otherwise the close stops, with an error wrapping
// ErrRemoteMoved: after offering to take the remote's commits when that can be
// done without a conflict, whatever the answer. What came in has to be looked
// at, and reviewed, before a close merges it.
//
// tree is the working tree that holds the branch (the main one when no
// worktree does); stop ends the caller's spinner before the question.
func checkRemoteBranch(
	ctx context.Context, deps closeDeps, tree *git.Client, picked *branch.Row, prompter ClosePrompter, stop func(),
) error {
	name := picked.BranchName
	rel, remoteRef, err := issueflow.RemoteOf(ctx, deps.client, name)
	if err != nil {
		return err //nolint:wrapcheck // names the command
	}
	if rel != issueflow.RemoteBehind && rel != issueflow.RemoteDiverged {
		return nil
	}

	localTip, _ := deps.client.ResolveRef("refs/heads/" + name)
	remoteTip, _ := deps.client.ResolveRef("refs/remotes/" + remoteRef)
	offer := RemoteOffer{
		Branch: name, RemoteRef: remoteRef, Merge: rel == issueflow.RemoteDiverged,
		LocalTip: shortSHA(localTip.String()), RemoteTip: shortSHA(remoteTip.String()),
	}
	if offer.Commits, err = deps.client.CommitsAhead(ctx, remoteRef, name); err != nil {
		return fmt.Errorf("count the commits of %s: %w", remoteRef, err)
	}
	take := "git merge --ff-only " + remoteRef
	if offer.Merge {
		take = "git merge " + remoteRef

		if oldRemote, own := rewrittenRemote(ctx, deps.client, name, remoteRef); oldRemote != "" {
			stop()
			printRewritten(deps.client.IO().Out, &offer, shortSHA(oldRemote), own)

			return ErrRemoteMoved
		}

		conflicts, err := deps.client.MergeDryRun(ctx, remoteRef, name)
		if err != nil {
			return fmt.Errorf("dry-run merge of %s: %w", remoteRef, err)
		}
		if len(conflicts) > 0 {
			return fmt.Errorf("%w: %q (%s) and %s (%s) have diverged, and merging them conflicts in: %s",
				ErrRemoteMoved, name, offer.LocalTip, remoteRef, offer.RemoteTip, strings.Join(conflicts, ", "))
		}
	}

	stop()
	out := deps.client.IO().Out

	yes, err := prompter.ConfirmTakeRemote(ctx, offer)
	if err != nil && !errors.Is(err, ErrCannotAsk) {
		return fmt.Errorf("confirm taking %s: %w", remoteRef, err)
	}
	if err != nil || !yes {
		fmt.Fprintf(out,
			"%s holds %d commit(s) branch %q lacks (yours: %s, remote: %s). Nothing was changed.\n"+
				"Take them on that branch, look at them, then run the close again:\n  %s\n",
			remoteRef, offer.Commits, name, offer.LocalTip, offer.RemoteTip, take)

		return ErrRemoteMoved
	}

	if err := takeRemote(ctx, tree, &offer); err != nil {
		return err
	}

	fmt.Fprintf(out, "Branch %q now holds the commits of %s. See what came in:\n  git log %s..%s\n%s\n",
		name, remoteRef, offer.LocalTip, name, afterTakingRemote(ctx, deps.client, picked))

	return ErrRemoteMoved
}

// maxRefLog bounds the reflog entries rewrittenRemote looks at.
const maxRefLog = 50

// rewrittenRemote tells a remote branch that was rewritten (a forced push)
// from one that only gained commits, for a local branch that diverged from
// it. It reads the reflog of the remote-tracking ref, so it knows only what
// this clone fetched or pushed. oldRemote is "" when nothing shows a rewrite;
// otherwise it is a position the remote branch once had and no longer
// contains, and own says whether the local branch holds commits on top of it
// (false: the local branch is an old copy, nothing in it is the user's alone).
func rewrittenRemote(ctx context.Context, c *git.Client, name, remoteRef string) (oldRemote string, own bool) {
	local, remote := "refs/heads/"+name, "refs/remotes/"+remoteRef
	// ponytail: up to three git processes per entry; batch with rev-list if a
	// tracking ref's reflog ever gets long enough to matter.
	entries := c.RefLog(ctx, remote)
	entries = entries[:min(len(entries), maxRefLog)]

	// The local branch is contained in an old position: an old copy. It is
	// not contained in the remote branch, so that position is gone from it.
	for _, old := range entries {
		if in, _ := c.IsAncestor(ctx, local, old); in {
			return old, false
		}
	}
	// The local branch is built on an old position the remote no longer holds.
	for _, old := range entries {
		if kept, _ := c.IsAncestor(ctx, old, remote); kept {
			continue
		}
		if under, _ := c.IsAncestor(ctx, old, local); under {
			return old, true
		}
	}

	return "", false
}

// printRewritten says that the offer's remote branch was rewritten and how to
// follow it. Nothing is merged: a merge would bring back what the rewrite
// dropped.
func printRewritten(out io.Writer, offer *RemoteOffer, oldRemote string, own bool) {
	fmt.Fprintf(out, "%s was rewritten (a forced push): it no longer holds %s, which it held before. "+
		"Branch %q (%s) was not changed.\n", offer.RemoteRef, oldRemote, offer.Branch, offer.LocalTip)

	if own {
		fmt.Fprintf(out,
			"Your branch holds commits of its own on top of the old history. Replay them on the rewritten branch, "+
				"then run the close again:\n  git rebase --onto %s %s %s\n",
			offer.RemoteRef, oldRemote, offer.Branch)

		return
	}

	fmt.Fprintf(out,
		"Your branch holds nothing the remote branch did not have before. See the commits the rewrite dropped, "+
			"some may be yours, pushed earlier:\n  git log %s..%s\n"+
			"To move your branch to the remote one (%s), then run the close again:\n  git checkout -B %s %s\n"+
			"It is at %s; to bring that back: git branch <name> %s\n",
		offer.RemoteRef, offer.Branch, offer.RemoteTip, offer.Branch, offer.RemoteRef, offer.LocalTip, offer.LocalTip)
}

// takeRemote fast-forwards or merges the offer's remote branch into its local
// one, in tree, which must be clean.
func takeRemote(ctx context.Context, tree *git.Client, offer *RemoteOffer) error {
	dirty, err := tree.IsDirty(ctx)
	if err != nil {
		return fmt.Errorf("status check: %w", err)
	}
	if dirty {
		return fmt.Errorf("%w: %s has uncommitted changes: commit or stash them, then run the close again",
			ErrRemoteMoved, tree.WorkingTreeRoot())
	}

	if !offer.Merge {
		if err := tree.FastForwardOnly(ctx, offer.RemoteRef, offer.Branch); err != nil {
			return fmt.Errorf("fast-forward %q to %s: %w", offer.Branch, offer.RemoteRef, err)
		}

		return nil
	}
	if err := tree.MergeForward(ctx, offer.RemoteRef, offer.Branch); err != nil {
		_ = tree.AbortMerge(ctx)

		return fmt.Errorf("merge %s into %q: %w", offer.RemoteRef, offer.Branch, err)
	}

	return nil
}

// afterTakingRemote says what to do once the branch took the remote's
// commits: it depends on the review of the issue, read best effort. The
// reviews are fetched first: the approval may have come with the commits.
func afterTakingRemote(ctx context.Context, c *git.Client, picked *branch.Row) string {
	const plain = "Look at them, then run the close again."

	_ = reviewpkg.Fetch(ctx, c, true)
	st, err := reviewpkg.Load(ctx, c, picked.IssueSlug)
	if err != nil || st == nil || st.Closed || st.Status != reviewpkg.StatusApproved {
		return plain
	}
	if reviewed, _ := issueflow.ReviewedBranch(ctx, c, st); reviewed != picked.BranchName {
		return plain
	}

	tip, _ := c.ResolveRef("refs/heads/" + picked.BranchName)
	if approved, err := issueflow.ApprovedCommit(ctx, c, st.Approvals); err == nil && approved == tip.String() {
		return "The branch is on the approved commit: run the close again."
	}

	return "The branch is past the approved commit: run git zf review request for a new round."
}
