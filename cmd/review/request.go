package review

import (
	"context"
	"fmt"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getRequestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "request",
		Short: "Submit an issue branch for code review (locks the branch)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewRequestInteractive(ctx, deps, newHuhReviewPrompter())
		},
	}
}

func runReviewRequestInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	branches, err := deps.store.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}
	if len(branches) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No in-progress branches to submit for review.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select branch to submit for review:", branches, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	return runReviewRequest(ctx, deps, picked.IssueSlug)
}

func runReviewRequest(ctx context.Context, deps reviewDeps, issueSlug string) error {
	// Guard: refuse if already in_review.
	latest, err := deps.store.GetLatestReview(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("get latest review: %w", err)
	}
	if latest != nil && latest.Status == store.ReviewStatusInReview {
		return fmt.Errorf("issue %q is already in review (round %d) — awaiting reviewer decision", issueSlug, latest.Round)
	}

	// Find the feature branch for this issue.
	branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	var featureBranch string
	for _, b := range branches {
		if b.IssueSlug == issueSlug && b.Status == store.BranchStatusInProgress {
			featureBranch = b.BranchName
			break
		}
	}
	if featureBranch == "" {
		return fmt.Errorf("no in-progress branch found for issue %q", issueSlug)
	}

	featureSHA, err := deps.client.ResolveRef("refs/heads/" + featureBranch)
	if err != nil {
		return fmt.Errorf("resolve feature branch HEAD: %w", err)
	}

	// Delete any stale review branch from a previous rejected round.
	reviewBranch := issueSlug + "@review"
	if exists, _ := deps.client.BranchExists(reviewBranch); exists {
		if err := deps.client.DeleteLocalBranch(ctx, reviewBranch, true); err != nil {
			fmt.Fprintf(deps.client.IO().Err, "warning: delete stale %s: %v\n", reviewBranch, err)
		}
		_ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
	}

	// Create review record in store.
	reviewRow, err := deps.store.InsertReview(ctx, issueSlug, "")
	if err != nil {
		return fmt.Errorf("insert review: %w", err)
	}

	// Write and push review ref (ref is the source of truth).
	ref := git.ReviewRef{
		Status:     string(store.ReviewStatusInReview),
		Round:      reviewRow.Round,
		FeatureSHA: featureSHA.String(),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	// First write: currentSHA is "" (no prior remote value). Pass "" to use the
	// unqualified lease form, which allows the push only if the remote has no
	// ref yet (preventing a second concurrent request from overwriting).
	if _, err := deps.client.WriteReviewRef(ctx, issueSlug, ref, ""); err != nil {
		return fmt.Errorf("write review ref: %w", err)
	}

	if err := deps.client.PushReviewRef(ctx, issueSlug, ""); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Issue %q is now in review (round %d). Branch %q is locked.\n"+
			"Share with your reviewer: git fetch && git zf review start\n"+
			"Tip: run 'git zf init' in every repo to install the push-lock guard.\n",
		issueSlug, reviewRow.Round, featureBranch)

	return nil
}
