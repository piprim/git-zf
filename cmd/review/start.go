package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/branch"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/spf13/cobra"
)

func (r Review) getStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Begin reviewing an issue (creates <IssueID>@review branch from the locked snapshot)",
		Args:  cobra.NoArgs,
		RunE: withDeps(r.appConfig, func(ctx context.Context, deps reviewDeps) error {
			return runReviewStartInteractive(ctx, deps, &huhReviewPrompter{})
		}),
	}
}

func runReviewStartInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	// inReviewBranches reads the review chains: the reviewer does not need
	// the branch tracked or checked out.
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
	ref, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil || ref.Closed {
		return fmt.Errorf("no review found for issue %q — has the developer run `git zf review request %s`?", issueSlug, issueSlug)
	}
	if ref.Status != reviewpkg.StatusInReview {
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

	recordReviewer(ctx, deps, ref)

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

// recordReviewer names this clone's user as the reviewer of ref's round: it
// writes the start op and pushes it, so the developer sees who started the
// review. No-op when the round already has a reviewer or git has no user
// configured. A failure is a warning: the review goes on without the name.
func recordReviewer(ctx context.Context, deps reviewDeps, ref *reviewpkg.State) {
	if reviewer, _ := deps.client.ConfigUser(ctx); reviewer == "" || ref.Reviewer != "" {
		return
	}

	startOp := &reviewpkg.Op{Type: reviewpkg.OpStart, Round: ref.Round}
	if err := reviewpkg.Append(ctx, deps.client, ref.Slug, startOp, false); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: record reviewer: %v\n", err)
	} else if err := reviewpkg.Push(ctx, deps.client, ref.Slug); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push reviewer identity: %v\n", err)
	}
}
