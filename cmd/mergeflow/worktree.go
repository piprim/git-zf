package mergeflow

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
)

// SourceTree resolves the working tree that has branch checked out and, when it
// is not the caller's own tree, opens a client on it for use as
// Params.SourceClient. Returns (nil, nil, nil) when the branch is not checked
// out anywhere, or when it is checked out in the caller's own tree (the engine
// then runs single-tree as before). The holding tree may be a linked worktree
// (issue close run from the main tree, or branch merge with a worktree-held
// source) or the main working tree (branch merge run from inside a linked
// worktree with a source the main tree holds): git refuses to check a branch
// out in two trees, so the merge must act where the branch already lives.
//
// A prunable entry (the directory is gone but git still records it) is an
// error: git keeps refusing to check out or delete the branch until the entry
// is pruned, so the Rebase strategy would fail later with a confusing checkout
// error. Failing here, before anything is touched, tells the user what to run.
func SourceTree(ctx context.Context, caller *git.Client, branch string) (*git.Client, *git.Worktree, error) {
	wt, err := caller.HoldingWorktree(ctx, branch)
	if err != nil {
		return nil, nil, fmt.Errorf("locate worktree for %q: %w", branch, err)
	}
	if wt == nil {
		return nil, nil, nil
	}
	if wt.Prunable {
		return nil, nil, fmt.Errorf(
			"branch %q is held by a stale worktree entry (%s): run `git worktree prune`, then retry",
			branch, wt.Path)
	}

	root, err := caller.WorkingTreeRoot()
	if err != nil {
		return nil, nil, fmt.Errorf("working tree root: %w", err)
	}
	if git.SamePath(wt.Path, root) {
		return nil, nil, nil
	}

	src, err := git.NewClientAt(caller.IO(), wt.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("open worktree %s: %w", wt.Path, err)
	}

	return src, wt, nil
}

// ConfirmRemoveFunc asks whether the worktree at path should be removed.
type ConfirmRemoveFunc func(ctx context.Context, path string) (bool, error)

// RemoveWorktreeStep is the post-merge step shared by issue close and branch
// merge: confirm, then `git worktree remove` (never --force). A refusal by git
// (modified or untracked files) is printed as a warning with a manual --force
// hint and is not an error: the merge already landed. The main working tree is
// never offered for removal (git refuses it, and the user's checkout lives
// there). invokedFrom is the working-tree root the command was typed in; when
// it is the removed worktree a cd hint back to the caller's tree is printed,
// since the shell now sits in a deleted directory. Returns whether the
// worktree was removed.
func RemoveWorktreeStep(
	ctx context.Context, caller *git.Client, wt *git.Worktree, invokedFrom string, confirm ConfirmRemoveFunc,
) (bool, error) {
	if wt.Main {
		fmt.Fprintf(caller.IO().Out, "Branch is checked out in the main working tree %q; leaving it in place.\n", wt.Path)

		return false, nil
	}

	ok, err := confirm(ctx, wt.Path)
	if err != nil {
		return false, err //nolint:wrapcheck // prompter already wraps
	}
	if !ok {
		fmt.Fprintf(caller.IO().Out, "Worktree %q kept.\n", wt.Path)

		return false, nil
	}

	// Resolve before removing: SamePath needs the directory to still exist to
	// see through symlinks.
	inside := invokedFrom != "" && git.SamePath(invokedFrom, wt.Path)

	if err := caller.RemoveWorktree(ctx, wt.Path); err != nil {
		fmt.Fprintf(caller.IO().Err,
			"warning: %v\nRemove it manually with: git worktree remove --force %q\n", err, wt.Path)

		return false, nil
	}

	fmt.Fprintf(caller.IO().Out, "Removed worktree %q.\n", wt.Path)

	if inside {
		if root, rerr := caller.WorkingTreeRoot(); rerr == nil {
			fmt.Fprintln(caller.IO().Out,
				tui.HintStyle.Render(fmt.Sprintf(
					"Run 'cd %q' — the worktree you were in has been removed.", root)))
		}
	}

	return true, nil
}
