package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/spf13/cobra"
)

// getGuardCommitCmd returns the internal `review guard-commit` command used by
// the pre-commit hook installed by `git zf init`. It exits non-zero when the
// currently checked-out feature branch has unincorporated reviewer commits.
// Hidden from help output. Fail-open on any setup error: a guard must never
// brick committing.
func (r Review) getGuardCommitCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "guard-commit",
		Short:  "Internal: block commits while reviewer commits await incorporation (used by pre-commit hook)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return nil // fail-open
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewGuardCommit(ctx, deps)
		},
	}
}

func runReviewGuardCommit(ctx context.Context, deps reviewDeps) error {
	pending, branchName, err := issueflow.PendingReviewForHEAD(ctx, deps.client, deps.store)
	if err != nil || pending == nil {
		return nil // fail-open
	}

	return fmt.Errorf(
		"commit blocked: %s has %d reviewer commit(s) not in %q (status: %s).\n"+
			"Run 'git zf review sync' to incorporate them first.\n"+
			"To bypass (not recommended): git commit --no-verify",
		pending.EffectiveRef, pending.Commits, branchName, pending.Status)
}
