package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
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
		Long: `Register the current branch into the git-zf store without creating a new branch.

Use this when you checked out a branch with plain 'git checkout' instead of
'git zf issue start' or 'git zf review start'.

  Developer (feature branch):  git checkout -b X.2@feat@part-two origin/X.2@feat@part-two
                                git zf review track
                                git zf review request

  Reviewer (review branch):    git checkout -b X.1@review <sha>
                                git zf review track
                                git zf review approve`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runTrack(ctx, deps)
		},
	}
}

func runTrack(ctx context.Context, deps reviewDeps) error {
	currentBranch, err := deps.client.CurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	if strings.HasSuffix(currentBranch, "@review") {
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
		&store.Issue{IDSlug: b.IssueID(), Title: title, StatusID: store.StatusIDInProgress},
		&store.Branch{Name: branchName, Type: b.Type(), StatusID: store.StatusIDInProgress},
	); err != nil {
		return fmt.Errorf("register branch in store: %w", err)
	}

	// Warn if a review ref already exists for this issue (branch is locked).
	if ref, _, _ := deps.client.ReadReviewRef(ctx, b.IssueID()); ref != nil &&
		ref.Status == string(store.ReviewStatusInReview) {
		fmt.Fprintf(deps.client.IO().Err,
			"Note: branch %q is currently locked for review (round %d).\n"+
				"You cannot submit for review again until the reviewer decides.\n",
			branchName, ref.Round)
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Branch %q is now tracked (issue %s, type %s).\n"+
			"Run: git zf review request\n",
		branchName, b.IssueID(), b.Type())

	return nil
}

// runTrackReviewer registers a manually-created review branch in the git-zf
// store so the reviewer can run approve/reject without having used review start.
func runTrackReviewer(ctx context.Context, deps reviewDeps, branchName string) error {
	issueSlug := strings.TrimSuffix(branchName, "@review")

	// Fetch review refs best-effort so we see the developer's lock signal.
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
	}

	// Verify the review ref exists and is in_review.
	ref, _, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil {
		return fmt.Errorf(
			"no review found for issue %q — has the developer run `git zf review request`?",
			issueSlug)
	}
	if ref.Status != string(store.ReviewStatusInReview) {
		return fmt.Errorf(
			"issue %q is not awaiting review (current status: %s)", issueSlug, ref.Status)
	}

	// Resolve reviewer identity from git config.
	reviewer, _ := deps.client.ConfigUser(ctx)

	// Check existing store record.
	latest, err := deps.store.GetLatestReview(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("check review record: %w", err)
	}

	switch {
	case latest != nil && latest.Reviewer != "":
		// Already registered.
		fmt.Fprintf(deps.client.IO().Out,
			"Already registered as reviewer for issue %q (round %d).\n",
			issueSlug, latest.Round)
		return nil

	case latest != nil && latest.Reviewer == "":
		// Record exists but reviewer identity not yet captured.
		if reviewer != "" {
			_ = deps.store.UpdateReviewerIdentity(ctx, latest.ID, reviewer)
		}

	default:
		// No record at all — insert one.
		var insertErr error
		latest, insertErr = deps.store.InsertReview(ctx, issueSlug, reviewer)
		if insertErr != nil {
			return fmt.Errorf("register review record: %w", insertErr)
		}
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Branch %q registered as review branch for issue %q (round %d).\n"+
			"Run:\n"+
			"  git zf review approve\n"+
			"  git zf review reject\n",
		branchName, issueSlug, latest.Round)

	return nil
}
