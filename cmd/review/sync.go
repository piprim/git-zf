package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Bring a branch up to date: merge pending reviewer commits, then parent drift",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewSyncInteractive(ctx, deps, newHuhReviewPrompter())
		},
	}
}

func runReviewSyncInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	// A sub-task closed in a sibling clone carries Merged=true on its branch ref
	// but may still show in_progress in this clone's store. Reconcile first so
	// the picker never offers an already-closed sub-task.
	issueflow.ReconcileMergedFromRefs(ctx, deps.store, deps.client)

	// Freshen refs so pending-review detection and parent drift see the
	// current remote state (best-effort; sync must work offline too).
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
	}
	if remote, _ := deps.client.Remote(); remote != "" {
		_ = deps.client.Fetch(ctx)
	}

	all, err := deps.store.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	// Candidates are branches with something to sync: a parent to drift
	// against, or reviewer commits pending incorporation.
	var candidates []store.BranchRow
	for _, b := range all {
		parent, pErr := deps.store.GetParentIssue(ctx, b.IssueSlug)
		hasParent := pErr == nil && parent != ""
		pending, _ := issueflow.PendingReviewCommits(ctx, deps.client, b.IssueSlug, b.BranchName)
		if hasParent || pending != nil {
			candidates = append(candidates, b)
		}
	}

	if len(candidates) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "Nothing to sync.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select branch to sync:", candidates, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	return runReviewSync(ctx, deps, picked.IssueSlug)
}

func runReviewSync(ctx context.Context, deps reviewDeps, issueSlug string) error {
	branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}
	var childBranch string
	for _, b := range branches {
		if b.IssueSlug == issueSlug {
			childBranch = b.BranchName
			break
		}
	}
	if childBranch == "" {
		return fmt.Errorf("no branch found for issue %q", issueSlug)
	}

	// Step 1 — incorporate reviewer commits from <slug>@review, if a decided
	// review says they are pending.
	pending, err := issueflow.PendingReviewCommits(ctx, deps.client, issueSlug, childBranch)
	if err != nil {
		return fmt.Errorf("detect pending review commits: %w", err)
	}
	if pending != nil {
		dirty, dErr := deps.client.IsDirty(ctx)
		if dErr != nil {
			return fmt.Errorf("status check: %w", dErr)
		}
		if dirty {
			return fmt.Errorf("working tree has uncommitted changes — cannot merge %s.\n"+
				"Run 'git stash', then 'git zf review sync', then 'git stash pop'", pending.EffectiveRef)
		}

		fmt.Fprintf(deps.client.IO().Out, "Merging %d reviewer commit(s) from %s into %q...\n",
			pending.Commits, pending.EffectiveRef, childBranch)

		if mErr := deps.client.MergeLeaveConflicts(ctx, pending.EffectiveRef, childBranch); mErr != nil {
			if errors.Is(mErr, git.ErrMergeConflicts) {
				fmt.Fprintf(deps.client.IO().Out,
					"Merge left in progress with conflicts.\n"+
						"Resolve the conflict markers, then run 'git zf commit' to conclude the merge.\n")
				return nil
			}
			return mErr
		}
		fmt.Fprintf(deps.client.IO().Out, "Reviewer commits incorporated into %q.\n", childBranch)
	}

	// Step 2 — parent integration drift (sub-tasks only; unchanged semantics).
	parentSlug, err := deps.store.GetParentIssue(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("get parent issue: %w", err)
	}
	if parentSlug == "" {
		if pending == nil {
			fmt.Fprintln(deps.client.IO().Out, "Nothing to sync.")
		}
		return nil
	}

	var parentBranch string
	for _, b := range branches {
		if b.IssueSlug == parentSlug {
			parentBranch = b.BranchName
			break
		}
	}
	if parentBranch == "" {
		return fmt.Errorf("no branch found for parent issue %q", parentSlug)
	}

	// Use origin/<parent> so we detect commits pushed by teammates (e.g. a
	// sibling sub-task close) that arrived via git fetch but have not yet
	// been fast-forwarded to the local branch.
	effectiveParent := parentBranch
	if remote, _ := deps.client.Remote(); remote != "" {
		effectiveParent = remote + "/" + parentBranch
	}

	behind, err := deps.client.CommitsAhead(ctx, effectiveParent, childBranch)
	if err != nil {
		return fmt.Errorf("check drift: %w", err)
	}
	if behind == 0 {
		fmt.Fprintf(deps.client.IO().Out, "Branch %q is already up to date with %q.\n",
			childBranch, parentBranch)
		return nil
	}

	fmt.Fprintf(deps.client.IO().Out, "Merging %q (%d new commit(s)) into %q...\n",
		parentBranch, behind, childBranch)

	if err := deps.client.MergeForward(ctx, effectiveParent, childBranch); err != nil {
		_ = deps.client.AbortMerge(ctx)
		return fmt.Errorf("merge %s into %s failed (conflicts detected — merge aborted): %w",
			parentBranch, childBranch, err)
	}

	fmt.Fprintf(deps.client.IO().Out, "Branch %q synced with %q.\n", childBranch, parentBranch)
	return nil
}
