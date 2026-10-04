package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Begin reviewing an issue (creates <IssueID>@review branch from the locked snapshot)",
		Args:  cobra.NoArgs,
		RunE: withDeps(r.appConfig, func(ctx context.Context, deps reviewDeps) error {
			return runReviewStartInteractive(ctx, deps, newHuhReviewPrompter())
		}),
	}
}

func runReviewStartInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	// inReviewBranches reads the review refs, not the local store, so the
	// reviewer does not need the branch registered in their own store.
	branches, err := inReviewBranches(ctx, deps)
	if err != nil {
		return err
	}
	if len(branches) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently awaiting review.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select issue to review:", branches, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	return runReviewStart(ctx, deps, picked.IssueSlug)
}

// runReviewStart creates the review branch for issueSlug. The caller is
// responsible for fetching review refs before calling this function.
func runReviewStart(ctx context.Context, deps reviewDeps, issueSlug string) error {
	ref, currentSHA, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil {
		return fmt.Errorf("no review found for issue %q — has the developer run `git zf review request %s`?", issueSlug, issueSlug)
	}
	if ref.Status != string(store.ReviewStatusInReview) {
		return fmt.Errorf("issue %q is not awaiting review (current status: %s)", issueSlug, ref.Status)
	}

	reviewBranch := branch.ReviewBranchName(issueSlug)
	if exists, _ := deps.client.BranchExists(reviewBranch); exists {
		return fmt.Errorf("branch %q already exists — review already started", reviewBranch)
	}

	root := deps.client.WorkingTreeRoot()

	// Fetch from the remote so the feature branch commits are present locally.
	// review start only fetched refs/zf/reviews/* earlier; the reviewer's clone
	// may not have the actual commit objects yet (e.g. a round-2 fix).
	if remote, _ := deps.client.Remote(); remote != "" {
		_ = deps.client.RunGitAt(ctx, root, "fetch", remote)
	}

	// Create review branch at the exact feature HEAD captured at lock time.
	if err := deps.client.RunGitAt(ctx, root, "checkout", "-b", reviewBranch, ref.FeatureSHA); err != nil {
		short := ref.FeatureSHA
		if len(short) > 7 {
			short = short[:7]
		}
		return fmt.Errorf("create review branch at %s: %w", short, err)
	}

	// Record reviewer identity in the ref (source of truth, visible cross-machine)
	// and in the local store (cache).
	if reviewer, _ := deps.client.ConfigUser(ctx); reviewer != "" {
		if ref.Reviewer == "" {
			updatedRef := *ref
			updatedRef.Reviewer = reviewer
			if _, writeErr := deps.client.WriteReviewRef(ctx, issueSlug, updatedRef, currentSHA); writeErr == nil {
				// Push so the developer can see who started the review.
				if pushErr := deps.client.PushReviewRef(ctx, issueSlug, currentSHA); pushErr != nil {
					fmt.Fprintf(deps.client.IO().Err, "warning: push reviewer identity: %v\n", pushErr)
				}
			}
		}
		if latest, err := deps.store.GetLatestReview(ctx, issueSlug); err == nil && latest != nil && latest.Reviewer == "" {
			_ = deps.store.UpdateReviewerIdentity(ctx, latest.ID, reviewer)
		}
	}

	featureSHAShort := ref.FeatureSHA
	if len(featureSHAShort) > 7 {
		featureSHAShort = featureSHAShort[:7]
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Created branch %q at %s (round %d).\n"+
			"Review the code, then run:\n"+
			"  git zf review approve %s\n"+
			"  git zf review reject %s\n",
		reviewBranch, featureSHAShort, ref.Round, issueSlug, issueSlug)

	return nil
}
