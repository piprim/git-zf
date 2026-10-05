package issueflow

import (
	"context"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/store"
)

// PendingReview describes reviewer commits on <slug>@review that a reviewer
// decision (approved / changes_requested) says the developer must incorporate
// into the feature branch.
type PendingReview struct {
	EffectiveRef string             // "42@review" or "origin/42@review"
	Commits      int                // commits ahead of the feature branch
	Status       store.ReviewStatus // approved | changes_requested
}

// ReviewBranchAhead finds the review branch that counts for slug and how many
// commits it has that featureBranch lacks. effective is "42@review" or
// "origin/42@review", "" when there is no review branch. It reads only local
// refs, whatever the state of the review.
func ReviewBranchAhead(
	ctx context.Context, client *git.Client, slug, featureBranch string,
) (effective string, n int, err error) {
	reviewBranch := branch.ReviewBranchName(slug)
	localExists, _ := client.BranchExists(reviewBranch)
	if localExists {
		effective = reviewBranch
	}
	if remote, _ := client.Remote(); remote != "" {
		candidate := remote + "/" + reviewBranch
		if _, refErr := client.ResolveRef("refs/remotes/" + candidate); refErr == nil {
			switch {
			case !localExists:
				effective = candidate
			default:
				// Both exist: if the remote-tracking ref carries commits the
				// local branch lacks, the reviewer pushed (or force-pushed)
				// after this checkout — their copy is authoritative. A local
				// branch ahead of the remote (the reviewer's own machine)
				// keeps winning. Offline: compares two already-fetched refs.
				if ahead, aErr := client.CommitsAhead(ctx, candidate, reviewBranch); aErr == nil && ahead > 0 {
					effective = candidate
				}
			}
		}
	}
	if effective == "" {
		return "", 0, nil
	}

	n, err = client.CommitsAhead(ctx, effective, featureBranch)

	return effective, n, err
}

// PendingReviewCommits reports reviewer commits awaiting incorporation for
// slug's featureBranch, or nil when nothing is pending. It reads only local
// refs — no network — so it is cheap enough for a pre-commit hook and works
// offline. The guard is armed only by a decided, open review: in_review means
// the reviewer hasn't decided, and a closed review or a stale review branch
// with no review never trips it.
func PendingReviewCommits(ctx context.Context, client *git.Client, slug, featureBranch string) (*PendingReview, error) {
	st, err := reviewpkg.Load(ctx, client, slug)
	if err != nil || st == nil || st.Closed {
		return nil, err
	}
	status := store.ReviewStatus(st.Status)
	if status != store.ReviewStatusApproved && status != store.ReviewStatusChangesRequested {
		return nil, nil
	}

	effective, n, err := ReviewBranchAhead(ctx, client, slug, featureBranch)
	if err != nil || n == 0 {
		return nil, err
	}

	return &PendingReview{EffectiveRef: effective, Commits: n, Status: status}, nil
}

// IssueSlugForBranch returns the issue slug owning branchName in the store,
// or "" when the branch is not tracked.
func IssueSlugForBranch(ctx context.Context, s *store.Store, branchName string) (string, error) {
	rows, err := s.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return "", err
	}
	for _, b := range rows {
		if b.BranchName == branchName {
			return b.IssueSlug, nil
		}
	}
	return "", nil
}

// PendingReviewForHEAD applies the commit-guard exemptions and returns the
// pending review for the currently checked-out branch, plus that branch name.
// It returns (nil, "", nil) whenever the guard must not trip: detached HEAD,
// an @review branch, a merge in progress (concluding a merge is exactly how
// incorporation happens), an untracked branch, or nothing pending.
func PendingReviewForHEAD(ctx context.Context, client *git.Client, s *store.Store) (*PendingReview, string, error) {
	branchName, err := client.CurrentBranch()
	if err != nil || branchName == "" {
		return nil, "", nil
	}
	if branch.IsReviewBranch(branchName) {
		return nil, "", nil
	}
	if inProgress, mhErr := client.MergeInProgress(); mhErr == nil && inProgress {
		return nil, "", nil
	}
	slug, err := IssueSlugForBranch(ctx, s, branchName)
	if err != nil || slug == "" {
		return nil, "", nil
	}
	pending, err := PendingReviewCommits(ctx, client, slug, branchName)
	if err != nil || pending == nil {
		return nil, "", nil
	}
	return pending, branchName, nil
}
