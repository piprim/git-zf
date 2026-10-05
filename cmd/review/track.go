package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

// TrackCmd returns the cobra command for both `git zf review track` and
// `git zf issue track`. Both command groups call this constructor — one
// implementation, two aliases in different namespaces.
func TrackCmd(appConfig *config.AppConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "track",
		Short: "Register the current branch in the git-zf store (for branches created with plain git checkout)",
		Long: `Register the current branch without creating a new branch: a feature branch
goes into the git-zf store, a review branch records you as the reviewer.

Use this when you checked out a branch with plain 'git checkout' instead of
'git zf issue start' or 'git zf review start'.

  Developer (feature branch):  git checkout -b X.2@feat@part-two origin/X.2@feat@part-two
                                git zf review track
                                git zf review request

  Reviewer (review branch):    git checkout -b X.1@review <sha>
                                git zf review track
                                git zf review approve`,
		Args: cobra.NoArgs,
		RunE: withDeps(appConfig, runTrack),
	}
}

func runTrack(ctx context.Context, deps reviewDeps) error {
	currentBranch, err := deps.client.CurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	if branch.IsReviewBranch(currentBranch) {
		return runTrackReviewer(ctx, deps, currentBranch)
	}

	if b, parseErr := branch.Parse(currentBranch); parseErr == nil {
		return runTrackDeveloper(ctx, deps, currentBranch, b)
	}

	return fmt.Errorf(
		"current branch %q does not match a git-zf naming convention\n"+
			"(expected <IssueID>@<type>@<slug> or <IssueID>@review)",
		currentBranch)
}

// runTrackDeveloper registers a feature branch that was checked out with plain
// git into the git-zf store as an in_progress issue branch.
func runTrackDeveloper(ctx context.Context, deps reviewDeps, branchName string, b *branch.Branch) error {
	// Idempotency: check if already tracked.
	rows, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}
	for _, r := range rows {
		if r.BranchName == branchName {
			fmt.Fprintf(deps.client.IO().Out,
				"Branch %q is already tracked (status: %s).\n", branchName, r.Status)
			return nil
		}
	}

	// Derive a human-readable title from the slug (replace hyphens with spaces).
	title := strings.ReplaceAll(b.Title(), "-", " ")

	if err := deps.store.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: b.IssueID(), Title: title},
		&store.Branch{Name: branchName, Type: b.Type(), StatusID: store.StatusIDInProgress},
	); err != nil {
		return fmt.Errorf("register branch in store: %w", err)
	}

	// Warn if a review ref already exists for this issue (branch is locked).
	if ref, _ := reviewpkg.Load(ctx, deps.client, b.IssueID()); ref != nil && !ref.Closed &&
		ref.Status == reviewpkg.StatusInReview {
		fmt.Fprintf(deps.client.IO().Err,
			"Note: branch %q is currently locked for review (round %d).\n"+
				"You cannot submit for review again until the reviewer decides.\n",
			branchName, ref.Round)
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Branch %q is now tracked (issue %s, type %s).\n"+
			"You can run 'git zf review request'\n",
		branchName, b.IssueID(), b.Type())

	return nil
}

// runTrackReviewer checks that a manually-created review branch has a review
// awaiting a decision and records the reviewer on its chain, as review start
// would have.
func runTrackReviewer(ctx context.Context, deps reviewDeps, branchName string) error {
	issueSlug, _ := branch.CutReviewSuffix(branchName)

	// Sync review refs best-effort so we see the developer's lock signal.
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}

	// Verify the review ref exists and is in_review.
	ref, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil {
		return fmt.Errorf(
			"no review found for issue %q — has the developer run `git zf review request`?",
			issueSlug)
	}
	if ref.Closed {
		return fmt.Errorf(
			"no open review for issue %q — has the developer run `git zf review request`?",
			issueSlug)
	}
	if ref.Status != reviewpkg.StatusInReview {
		return fmt.Errorf(
			"issue %q is not awaiting review (current status: %s)", issueSlug, ref.Status)
	}

	recordReviewer(ctx, deps, ref)

	fmt.Fprintf(deps.client.IO().Out,
		"Branch %q registered as review branch for issue %q (round %d).\n"+
			"Run:\n"+
			"  git zf review approve\n"+
			"  git zf review reject\n",
		branchName, issueSlug, ref.Round)

	return nil
}
