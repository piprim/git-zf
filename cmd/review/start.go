package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Begin reviewing an issue (creates <IssueID>@review branch from the locked snapshot)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewStartInteractive(ctx, deps, newHuhReviewPrompter())
		},
	}
}

func runReviewStartInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
	}

	// Collect issue slugs with in_review refs by scanning the store.
	branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	var inReviewSlugs []string
	for _, b := range branches {
		ref, _, refErr := deps.client.ReadReviewRef(ctx, b.IssueSlug)
		if refErr == nil && ref != nil && ref.Status == string(store.ReviewStatusInReview) {
			inReviewSlugs = append(inReviewSlugs, b.IssueSlug)
		}
	}

	if len(inReviewSlugs) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently awaiting review.")
		return nil
	}

	issueSlug, err := prompter.PickIssueToStart(ctx, inReviewSlugs)
	if err != nil {
		return fmt.Errorf("issue picker: %w", err)
	}
	if issueSlug == "" {
		return nil
	}

	return runReviewStart(ctx, deps, issueSlug)
}

// runReviewStart creates the review branch for issueSlug. The caller is
// responsible for fetching review refs before calling this function.
func runReviewStart(ctx context.Context, deps reviewDeps, issueSlug string) error {
	ref, _, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil {
		return fmt.Errorf("no review found for issue %q — has the developer run `git zf review request %s`?", issueSlug, issueSlug)
	}
	if ref.Status != string(store.ReviewStatusInReview) {
		return fmt.Errorf("issue %q is not awaiting review (current status: %s)", issueSlug, ref.Status)
	}

	reviewBranch := issueSlug + "@review"
	if exists, _ := deps.client.BranchExists(reviewBranch); exists {
		return fmt.Errorf("branch %q already exists — review already started", reviewBranch)
	}

	root, err := deps.client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	// Create review branch at the exact feature HEAD captured at lock time.
	if err := deps.client.RunGitAt(ctx, root, "checkout", "-b", reviewBranch, ref.FeatureSHA); err != nil {
		short := ref.FeatureSHA
		if len(short) > 7 {
			short = short[:7]
		}
		return fmt.Errorf("create review branch at %s: %w", short, err)
	}

	// Record reviewer identity in local store (best-effort).
	if reviewer, _ := deps.client.ConfigUser(ctx); reviewer != "" {
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
