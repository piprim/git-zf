package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Merge the parent integration branch into a drifted sub-task branch",
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

	all, err := deps.store.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	// Filter to branches that are children of some parent issue.
	var subtasks []store.BranchRow
	for _, b := range all {
		parent, err := deps.store.GetParentIssue(ctx, b.IssueSlug)
		if err == nil && parent != "" {
			subtasks = append(subtasks, b)
		}
	}

	if len(subtasks) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No sub-task branches found.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select sub-task branch to sync with its parent:", subtasks, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	return runReviewSync(ctx, deps, picked.IssueSlug)
}

func runReviewSync(ctx context.Context, deps reviewDeps, issueSlug string) error {
	parentSlug, err := deps.store.GetParentIssue(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("get parent issue: %w", err)
	}
	if parentSlug == "" {
		return fmt.Errorf("issue %q has no parent — sync is only for sub-tasks", issueSlug)
	}

	var childBranch, parentBranch string
	branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}
	for _, b := range branches {
		switch b.IssueSlug {
		case issueSlug:
			childBranch = b.BranchName
		case parentSlug:
			parentBranch = b.BranchName
		}
	}
	if childBranch == "" {
		return fmt.Errorf("no branch found for sub-task issue %q", issueSlug)
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
