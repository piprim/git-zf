package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/cmd/pushflow"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/spf13/cobra"
)

func (r Review) getApproveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve",
		Short: "Approve a review — signals the branch is ready to close",
		Long: `Pick a review you started and approve it. Its status becomes "approved"
and the developer can run git zf issue close, which incorporates any commit
you left on <ID>@review before merging.

With require-signed = true under [review] in the config, the approval is a
signed commit, and git zf issue close refuses to merge without an approval
that git verify-commit accepts and that covers the commit being merged.

The review record is pushed (--push / --no-push skip the proposal), and when
the issue came from a tracker a status update is proposed.`,
		Args: cobra.NoArgs,
		RunE: withDeps(r.appConfig, func(ctx context.Context, deps reviewDeps) error {
			return runReviewApproveInteractive(ctx, deps, &huhReviewPrompter{})
		}),
	}
	pushflow.AddFlags(cmd)
	return cmd
}

func runReviewApproveInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	branches, err := inReviewBranches(ctx, deps)
	if err != nil {
		return err
	}
	if len(branches) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently in review.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select issue to approve:", branches, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	if err := runReviewApprove(ctx, deps, picked.IssueSlug); err != nil {
		return err
	}
	maybeUpdateTrackerStatus(ctx, deps, prompter, picked.IssueSlug)
	return nil
}

func runReviewApprove(ctx context.Context, deps reviewDeps, issueSlug string) error {
	d, err := recordReviewDecision(ctx, deps, issueSlug, reviewpkg.StatusApproved, "")
	if err != nil {
		return err
	}

	msg := fmt.Sprintf("Issue %q approved (round %d).", issueSlug, d.round)
	if d.hasCommits {
		msg += fmt.Sprintf(" Reviewer pushed commits to %s — they will be incorporated on close.", d.reviewBranch)
	}
	fmt.Fprintln(deps.client.IO().Out, msg)
	fmt.Fprintf(deps.client.IO().Out, "Issue %q is ready to close. Developer can now run: git zf issue close\n", issueSlug)

	if d.hasCommits {
		if err := proposeReviewPush(ctx, deps, d.reviewBranch); err != nil {
			return err
		}
	}

	return nil
}
