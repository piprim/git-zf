package review

import (
	"context"
	"fmt"
	"time"

	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getApproveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve",
		Short: "Approve a review — signals the branch is ready to close",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewApproveInteractive(ctx, deps, newHuhReviewPrompter())
		},
	}
	pushflow.AddFlags(cmd)
	return cmd
}

func runReviewApproveInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	branches, err := inReviewBranches(ctx, deps)
	if err != nil {
		return err
	}
	if len(branches) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently in review.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select issue to approve:", branches, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	if err := runReviewApprove(ctx, deps, picked.IssueSlug); err != nil {
		return err
	}
	maybeUpdateTrackerStatus(ctx, deps, prompter, picked.IssueSlug)
	return nil
}

func runReviewApprove(ctx context.Context, deps reviewDeps, issueSlug string) error {
	latest, err := ensureReviewRecord(ctx, deps, issueSlug)
	if err != nil {
		return err
	}
	if latest.Status != store.ReviewStatusInReview {
		return fmt.Errorf("issue %q is not in review (current status: %s)", issueSlug, latest.Status)
	}

	// Detect whether reviewer pushed commits to <issueSlug>@review.
	reviewBranch := issueSlug + "@review"
	hasCommits := false

	branches, branchErr := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if branchErr != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: list branches: %v (has_commits will be false)\n", branchErr)
	}
	for _, b := range branches {
		if b.IssueSlug == issueSlug {
			if exists, _ := deps.client.BranchExists(reviewBranch); exists {
				n, countErr := deps.client.CommitsAhead(ctx, reviewBranch, b.BranchName)
				if countErr == nil && n > 0 {
					hasCommits = true
				}
			}
			break
		}
	}

	// Write and push the ref FIRST (ref is the source of truth).
	// Update the store after; a store failure leaves the ref correct and close
	// will work. A ref failure leaves the store unchanged (consistent).
	currentRef, currentSHA, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}

	featureSHA := ""
	if currentRef != nil {
		featureSHA = currentRef.FeatureSHA
	}

	reviewer := ""
	if currentRef != nil {
		reviewer = currentRef.Reviewer
	}
	newRef := git.ReviewRef{
		Status:     string(store.ReviewStatusApproved),
		Round:      latest.Round,
		FeatureSHA: featureSHA,
		Reviewer:   reviewer,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	if _, err := deps.client.WriteReviewRef(ctx, issueSlug, newRef, currentSHA); err != nil {
		return fmt.Errorf("write review ref: %w", err)
	}

	// expectedOldSHA is currentSHA — the value the remote has before this push.
	// newSHA is what we just wrote locally; the remote has never seen it yet.
	if err := deps.client.PushReviewRef(ctx, issueSlug, currentSHA); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
	}

	if err := deps.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatusApproved, hasCommits); err != nil {
		return fmt.Errorf("update review status: %w", err)
	}

	msg := fmt.Sprintf("Issue %q approved (round %d).", issueSlug, latest.Round)
	if hasCommits {
		msg += fmt.Sprintf(" Reviewer pushed commits to %s — they will be incorporated on close.", reviewBranch)
	}
	fmt.Fprintln(deps.client.IO().Out, msg)
	fmt.Fprintf(deps.client.IO().Out, "Issue %q is ready to close. Developer can now run: git zf issue close\n", issueSlug)

	if exists, _ := deps.client.BranchExists(reviewBranch); exists && hasCommits {
		if err := proposeReviewPush(ctx, deps, reviewBranch); err != nil {
			return err
		}
	}

	return nil
}
