package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/spf13/cobra"
)

func (r Review) getSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Bring a sub-task branch up to date with its parent branch",
		Long: `Pick an in-progress sub-task branch (the current one is pre-selected): the
parent branch (<remote>/<parent>) is merged into it. A conflict is aborted
and reported.`,
		Args: cobra.NoArgs,
		RunE: withDeps(r.appConfig, func(ctx context.Context, deps reviewDeps) error {
			return runReviewSyncInteractive(ctx, deps, &huhReviewPrompter{})
		}),
	}
}

func runReviewSyncInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	// A sub-task closed in a sibling clone is still in progress here until the
	// chains are fetched: do it first so the picker never offers it.
	fetchBranchRefs(ctx, deps)

	// Freshen refs so the parent drift sees the current remote state
	// (best-effort; sync must work offline too).
	if remote, _ := deps.client.Remote(); remote != "" {
		_ = deps.client.Fetch(ctx)
	}

	all, err := issueflow.LocalInProgress(ctx, deps.client)
	if err != nil {
		return err //nolint:wrapcheck // already says what was listed
	}

	// Candidates are the branches with a parent to drift against.
	var candidates []branch.Row
	for _, b := range all {
		if st, pErr := branch.Load(ctx, deps.client, b.IssueSlug); pErr == nil && st != nil && st.Parent != "" {
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

	return runReviewSync(ctx, deps, picked.IssueSlug, picked.BranchName)
}

// runReviewSync merges the parent branch of issueSlug into childBranch, the
// branch of that issue the user picked.
func runReviewSync(ctx context.Context, deps reviewDeps, issueSlug, childBranch string) error {
	parentSlug := ""
	if st, err := branch.Load(ctx, deps.client, issueSlug); err != nil {
		return fmt.Errorf("get parent issue: %w", err)
	} else if st != nil {
		parentSlug = st.Parent
	}
	if parentSlug == "" {
		fmt.Fprintln(deps.client.IO().Out, "Nothing to sync.")
		return nil
	}

	// A parent still in the old blob format is not "no parent": the drift
	// check would silently be skipped.
	if _, err := branch.Load(ctx, deps.client, parentSlug); errors.Is(err, branch.ErrLegacyBranch) {
		return fmt.Errorf(
			"parent issue %q of %q is tracked in the old format.\n"+
				"Check out its branch and run `git zf issue track`: %w", parentSlug, issueSlug, err)
	}

	parentBranch, err := issueflow.BranchNameForIssue(ctx, deps.client, parentSlug)
	if err != nil {
		return err
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
