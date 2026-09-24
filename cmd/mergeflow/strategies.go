package mergeflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/convert"
)

// rebasePlan captures the state computed by rebasePreflight and consumed by the
// rebase/classic executors. remoteBase is "<remote>/<target>" when a remote is
// configured, otherwise "<target>"; remoteName is "" in the no-remote case so
// callers can pick the correct ref namespace.
type rebasePlan struct {
	featureOrigSHA plumbing.Hash
	remoteName     string
	remoteBase     string
}

// squash resolves the source and target tip SHAs, runs `git merge --squash`
// (which stages the merge but does not commit), then composes and commits.
// Esc/Ctrl+C in the form aborts; for a materialized source the staged changes
// are discarded (the branch is disposable and the diff is reproducible from
// origin), otherwise they are left in place for inspection.
func (r *run) squash(ctx context.Context) (err error) {
	branchHash, err := r.client.ResolveRef("refs/heads/" + r.source)
	if err != nil {
		return fmt.Errorf("resolve branch %q: %w", r.source, err)
	}

	// Fast-forward local target to origin/<target> before squashing so the
	// squash commit lands on the current remote tip (no-op when no remote).
	if remote, _ := r.client.Remote(); remote != "" {
		_ = r.client.FastForwardOnly(ctx, remote+"/"+r.target, r.target)
	}

	baseHash, err := r.client.ResolveBranchRef(r.target)
	if err != nil {
		return fmt.Errorf("resolve base %q: %w", r.target, err)
	}

	if err := r.client.MergeSquash(ctx, r.source, r.target); err != nil {
		return fmt.Errorf("merge squash: %w", err)
	}

	// From here on the squash diff is staged on target. On abort (compose Esc,
	// commit failure) discard it for a materialized source so the outer rollback
	// really does leave the clone as it was found.
	defer func() {
		if err == nil || !r.materialized {
			return
		}
		if rbErr := r.client.ResetHard(ctx, baseHash.String()); rbErr != nil {
			fmt.Fprintf(r.client.IO().Err, "warning: discard staged squash changes: %v\n", rbErr)

			return
		}
		fmt.Fprintf(r.client.IO().Err,
			"Rolled back: staged squash changes on %q discarded\n", r.target)
	}()

	prefill := r.prefill(commit.MergeStrategySquash, branchHash, baseHash)

	return r.composeAndCommit(ctx, r.client, prefill, commit.MergeStrategySquash)
}

// rebase pre-flights the working tree, performs a real rebase of source onto
// target, soft-resets so the merged diff is staged, composes+commits, and
// fast-forwards target. Rollback via a named-return closure: any failure
// between the rebase and a successful commit triggers `git reset --hard
// <featureOrigSHA>`. A post-commit FF failure is signalled with
// errFastForwardDeferred so the caller keeps the new commit.
//
// Note: MergeRebase runs `git merge <base>` on r.src, so a conflict there would
// leave MERGE_HEAD in the SOURCE worktree, not in the main tree — the user's own
// directory, which is where they would resolve it. rebasePreflight's merge
// dry-run is what keeps that from happening in practice: it fails the whole
// strategy before anything is checked out or merged.
func (r *run) rebase(ctx context.Context) (err error) {
	plan, err := r.rebasePreflight(ctx, r.src)
	if err != nil {
		return err
	}

	// The final fast-forward checks out target on the main tree, so that tree
	// must be clean too when the source lives in a separate worktree.
	if r.src != r.client {
		dirty, err := r.client.IsDirty(ctx)
		if err != nil {
			return fmt.Errorf("dirty check (main tree): %w", err)
		}

		if dirty {
			return errors.New("main working tree has uncommitted modifications — commit or stash before merging")
		}
	}

	// MergeRebase checks out the source itself; on a worktree client that is
	// a no-op because the worktree already has it checked out.
	if err := r.src.MergeRebase(ctx, r.source, r.target); err != nil {
		return fmt.Errorf("merge rebase: %w", err)
	}

	defer func() {
		if err == nil || errors.Is(err, errFastForwardDeferred) {
			return
		}

		if rbErr := r.src.ResetHard(ctx, plan.featureOrigSHA.String()); rbErr != nil {
			err = fmt.Errorf("rollback after %w failed: %v", err, rbErr)

			return
		}

		fmt.Fprintf(r.client.IO().Err,
			"Rolled back: feature branch %q restored to %s\n",
			r.source, plan.featureOrigSHA.String()[:7])
	}()

	baseRef := "refs/remotes/" + plan.remoteBase
	if plan.remoteName == "" {
		baseRef = "refs/heads/" + r.target
	}
	baseOriginSHA, err := r.client.ResolveRef(baseRef)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", baseRef, err)
	}

	prefill := r.prefill(commit.MergeStrategyRebase, plan.featureOrigSHA, baseOriginSHA)
	if err := r.composeAndCommit(ctx, r.src, prefill, commit.MergeStrategyRebase); err != nil {
		return err
	}

	if ffErr := r.client.FastForwardOnly(ctx, r.source, r.target); ffErr != nil {
		fmt.Fprintf(r.client.IO().Err,
			"Commit created on %q but local %s has diverged from %s.\n"+
				"Run `git pull --ff-only` on %s, then `git merge --ff-only %s` to land it.\n",
			r.source, r.target, plan.remoteBase,
			r.target, r.source)

		return errFastForwardDeferred
	}

	return nil
}

// classic drives the Classic strategy: shared rebasePreflight, FF-sync of local
// target against origin/<target> (or direct checkout when no remote), a real
// --no-ff --no-commit merge on target, compose+commit. Rollback on any failure
// between MergeNoFFNoCommit and a successful Commit runs `git merge --abort`.
func (r *run) classic(ctx context.Context) (err error) {
	plan, err := r.rebasePreflight(ctx, r.client)
	if err != nil {
		return err
	}

	if plan.remoteName != "" {
		if err := r.client.FastForwardOnly(ctx, plan.remoteBase, r.target); err != nil {
			return fmt.Errorf("local %s diverged from %s — `git pull --ff-only` first: %w",
				r.target, plan.remoteBase, err)
		}
	} else {
		if err := r.client.Checkout(ctx, r.target); err != nil {
			return fmt.Errorf("checkout %s: %w", r.target, err)
		}
	}

	baseSHA, err := r.client.ResolveRef("refs/heads/" + r.target)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", r.target, err)
	}

	if err := r.client.MergeNoFFNoCommit(ctx, r.source, r.target); err != nil {
		return fmt.Errorf("merge --no-ff --no-commit: %w", err)
	}

	defer func() {
		if err == nil {
			return
		}

		if abErr := r.client.AbortMerge(ctx); abErr != nil {
			err = fmt.Errorf("merge --abort after %w failed: %v", err, abErr)

			return
		}

		fmt.Fprintf(r.client.IO().Err,
			"Rolled back: working tree on %q restored to pre-merge state\n",
			r.target)
	}()

	prefill := r.prefill(commit.MergeStrategyClassic, plan.featureOrigSHA, baseSHA)

	// No post-commit fast-forward: the merge commit lands directly on target
	// (MergeNoFFNoCommit checked out target before merging).
	return r.composeAndCommit(ctx, r.client, prefill, commit.MergeStrategyClassic)
}

// rebasePreflight runs the read-only checks that precede a Rebase or Classic
// merge: dirty check on tree (the working tree the strategy is about to
// modify), resolve the source tip by ref, remote, fetch, ancestor check, and
// merge dry-run. It never checks out anything: the source may live in a
// linked worktree that the main tree cannot check out.
func (r *run) rebasePreflight(ctx context.Context, tree *git.Client) (rebasePlan, error) {
	dirty, err := tree.IsDirty(ctx)
	if err != nil {
		return rebasePlan{}, fmt.Errorf("dirty check: %w", err)
	}

	if dirty {
		return rebasePlan{},
			errors.New("working tree has uncommitted modifications — commit or stash before merging")
	}

	featureOrigSHA, err := r.client.ResolveRef("refs/heads/" + r.source)
	if err != nil {
		return rebasePlan{}, fmt.Errorf("resolve %s: %w", r.source, err)
	}

	remoteName, err := r.client.Remote()
	if err != nil {
		return rebasePlan{}, fmt.Errorf("resolve remote: %w", err)
	}

	if err := r.client.Fetch(ctx); err != nil {
		return rebasePlan{}, fmt.Errorf("fetch: %w", err)
	}

	remoteBase := r.target
	if remoteName != "" {
		remoteBase = remoteName + "/" + r.target
	}

	integrated, err := r.client.IsAncestor(ctx, r.source, remoteBase)
	if err != nil {
		return rebasePlan{}, fmt.Errorf("ancestor check: %w", err)
	}

	if integrated {
		return rebasePlan{}, fmt.Errorf("%q has no commits ahead of %s",
			r.source, remoteBase)
	}

	if err := r.mergeDryRun(ctx, remoteBase); err != nil {
		return rebasePlan{}, err
	}

	return rebasePlan{
		featureOrigSHA: featureOrigSHA,
		remoteName:     remoteName,
		remoteBase:     remoteBase,
	}, nil
}

func (r *run) mergeDryRun(ctx context.Context, remoteBase string) error {
	conflicts, err := r.client.MergeDryRun(ctx, r.source, remoteBase)
	if err != nil {
		return fmt.Errorf("merge dry-run: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Fprintln(r.client.IO().Out, "Conflicts detected:")
		for _, f := range conflicts {
			fmt.Fprintln(r.client.IO().Out, "  "+f)
		}

		return fmt.Errorf("merge conflicts vs %s in %q", remoteBase, r.source)
	}

	return nil
}

// composeAndCommit drives the commit-message form with the caller-supplied
// prefill and commits the staged merge on tree (the client whose working tree
// holds the staged result: src for Rebase, main for Squash/Classic).
func (r *run) composeAndCommit(
	ctx context.Context, tree *git.Client, prefill map[string]any, strategy commit.MergeStrategy,
) error {
	msg, opts, err := r.prompter.ComposeMessage(ctx, prefill)
	if err != nil {
		return err //nolint:wrapcheck // prompter already wraps
	}

	if err := tree.Commit(ctx, msg, convert.CommitOptionsFromTUI(opts)); err != nil {
		return fmt.Errorf("commit %s: %w", strategy, err)
	}

	return nil
}
