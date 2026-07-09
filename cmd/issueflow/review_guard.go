package issueflow

import (
	"context"
	"strings"

	"github.com/piprim/git-zf/git"
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

// PendingReviewCommits reports reviewer commits awaiting incorporation for
// slug's featureBranch, or nil when nothing is pending. It reads only local
// refs — no network — so it is cheap enough for a pre-commit hook and works
// offline. The guard is armed only by a decided review ref: in_review means
// the reviewer hasn't decided, and a stale review branch with no ref (e.g.
// after a close) never trips it.
func PendingReviewCommits(ctx context.Context, client *git.Client, slug, featureBranch string) (*PendingReview, error) {
	ref, _, err := client.ReadReviewRef(ctx, slug)
	if err != nil || ref == nil {
		return nil, err
	}
	status := store.ReviewStatus(ref.Status)
	if status != store.ReviewStatusApproved && status != store.ReviewStatusChangesRequested {
		return nil, nil
	}

	reviewBranch := slug + "@review"
	effective := ""
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
		return nil, nil
	}

	n, err := client.CommitsAhead(ctx, effective, featureBranch)
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
	if strings.HasSuffix(branchName, "@review") {
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
