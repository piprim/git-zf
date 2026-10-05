package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/branch"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/spf13/cobra"
)

// getGuardCmd returns the internal `review guard <branch>` command used by the
// pre-push hook. It exits 1 with a message when the branch is locked for review.
// Hidden from help output.
func (r Review) getGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "guard <branch>",
		Short:  "Internal: check whether a branch is locked for review (used by pre-push hook)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				// Fail-open: if the repository can't be opened, allow the push.
				return nil
			}
			return runReviewGuard(ctx, deps, args[0])
		},
	}
}

func runReviewGuard(ctx context.Context, deps reviewDeps, branchName string) error {
	// Reviewer's own branch — always allow.
	if branch.IsReviewBranch(branchName) {
		return nil
	}

	// A branch git-zf does not track is not locked.
	st, _, err := branch.Find(ctx, deps.client, branchName)
	if err != nil || st == nil {
		return nil // fail-open
	}
	issueSlug := st.Slug

	// Fetch the latest decision for this issue before checking — the reviewer
	// may have approved or rejected after the developer last fetched. Silent
	// and best-effort: if the fetch fails we fall back to the local chain.
	_ = reviewpkg.Fetch(ctx, deps.client, true)

	ref, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil || ref == nil || ref.Closed {
		return nil // fail-open, legacy blob included
	}

	if ref.Status == reviewpkg.StatusInReview {
		return fmt.Errorf(
			"push blocked: branch %q is locked for code review (issue %q, round %d).\n"+
				"Wait for the reviewer to approve or reject before pushing.\n"+
				"To bypass (not recommended): git push --no-verify",
			branchName, issueSlug, ref.Round)
	}

	return nil
}
