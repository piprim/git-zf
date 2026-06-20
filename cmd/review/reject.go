package review

import (
	"context"
	"fmt"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getRejectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reject",
		Short: "Request changes on a review — unlocks the branch for the next iteration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewRejectInteractive(ctx, deps, newHuhReviewPrompter())
		},
	}
}

func runReviewRejectInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	branches, err := inReviewBranches(ctx, deps)
	if err != nil {
		return err
	}
	if len(branches) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently in review.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select issue to reject:", branches, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	return runReviewReject(ctx, deps, picked.IssueSlug)
}

func runReviewReject(ctx context.Context, deps reviewDeps, issueSlug string) error {
	latest, err := deps.store.GetLatestReview(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("get latest review: %w", err)
	}
	if latest == nil {
		return fmt.Errorf("no review found for issue %q", issueSlug)
	}
	if latest.Status != store.ReviewStatusInReview {
		return fmt.Errorf("issue %q is not in review (current status: %s)", issueSlug, latest.Status)
	}

	// Detect reviewer commits on <issueSlug>@review.
	reviewBranch := issueSlug + "@review"
	var featureBranch string

	branches, branchErr := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if branchErr != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: list branches: %v (has_commits will be false)\n", branchErr)
	}
	for _, b := range branches {
		if b.IssueSlug == issueSlug {
			featureBranch = b.BranchName
			break
		}
	}

	hasCommits := false
	reviewBranchExists := false
	if exists, _ := deps.client.BranchExists(reviewBranch); exists {
		reviewBranchExists = true
		if featureBranch != "" {
			n, countErr := deps.client.CommitsAhead(ctx, reviewBranch, featureBranch)
			if countErr == nil && n > 0 {
				hasCommits = true
			}
		}
	}

	// Write and push the ref FIRST (ref is the source of truth).
	currentRef, currentSHA, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	featureSHA := ""
	if currentRef != nil {
		featureSHA = currentRef.FeatureSHA
	}

	newRef := git.ReviewRef{
		Status:     string(store.ReviewStatusChangesRequested),
		Round:      latest.Round,
		FeatureSHA: featureSHA,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	if _, err := deps.client.WriteReviewRef(ctx, issueSlug, newRef, currentSHA); err != nil {
		return fmt.Errorf("write review ref: %w", err)
	}
	// expectedOldSHA is currentSHA — the value the remote currently has.
	if err := deps.client.PushReviewRef(ctx, issueSlug, currentSHA); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
	}

	if err := deps.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatusChangesRequested, hasCommits); err != nil {
		return fmt.Errorf("update review status: %w", err)
	}

	// Handle review branch: keep if reviewer pushed commits, delete if empty.
	if reviewBranchExists && !hasCommits {
		if err := deps.client.DeleteLocalBranch(ctx, reviewBranch, true); err != nil {
			fmt.Fprintf(deps.client.IO().Err, "warning: delete %s: %v\n", reviewBranch, err)
		}
		_ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
		fmt.Fprintf(deps.client.IO().Out,
			"Issue %q: changes requested (round %d). Branch %q unlocked.\n",
			issueSlug, latest.Round, featureBranch)
		return nil
	}

	if hasCommits {
		n, _ := deps.client.CommitsAhead(ctx, reviewBranch, featureBranch)
		fmt.Fprintf(deps.client.IO().Out,
			"Issue %q: changes requested (round %d). Branch %q unlocked.\n"+
				"%s has %d reviewer commit(s). Inspect with:\n"+
				"  git log %s..%s\n"+
				"Cherry-pick, adapt, or discard as needed, then:\n"+
				"  git zf review request %s\n",
			issueSlug, latest.Round, featureBranch,
			reviewBranch, n, featureBranch, reviewBranch, issueSlug)
		return nil
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Issue %q: changes requested (round %d). Branch %q unlocked.\n",
		issueSlug, latest.Round, featureBranch)

	return nil
}
