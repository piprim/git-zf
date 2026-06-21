package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/store"
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
				// Fail-open: if store can't be opened, allow the push.
				return nil
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewGuard(ctx, deps, args[0])
		},
	}
}

func runReviewGuard(ctx context.Context, deps reviewDeps, branchName string) error {
	// Reviewer's own branch — always allow.
	if strings.HasSuffix(branchName, "@review") {
		return nil
	}

	// Look up the branch in the store to get the issue slug.
	branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return nil // fail-open on store error
	}

	var issueSlug string
	for _, b := range branches {
		if b.BranchName == branchName {
			issueSlug = b.IssueSlug
			break
		}
	}
	if issueSlug == "" {
		return nil // not a tracked branch — allow
	}

	// Fetch the latest decision for this issue before checking — the reviewer
	// may have approved or rejected after the developer last fetched. Silent
	// best-effort: if the fetch fails we fall back to the local ref.
	deps.client.FetchReviewRef(ctx, issueSlug)

	ref, _, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil || ref == nil {
		return nil
	}

	if ref.Status == string(store.ReviewStatusInReview) {
		return fmt.Errorf(
			"push blocked: branch %q is locked for code review (issue %q, round %d).\n"+
				"Wait for the reviewer to approve or reject before pushing.\n"+
				"To bypass (not recommended): git push --no-verify",
			branchName, issueSlug, ref.Round)
	}

	return nil
}
