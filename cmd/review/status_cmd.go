package review

import (
	"context"
	"fmt"

	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the full review history for an issue",
		Args:  cobra.NoArgs,
		RunE: withDeps(r.appConfig, func(ctx context.Context, deps reviewDeps) error {
			return runReviewStatusInteractive(ctx, deps, &huhReviewPrompter{})
		}),
	}
}

func runReviewStatusInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}

	// Show branches that have any review history.
	all, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	var withHistory []store.BranchRow
	for _, b := range all {
		rows, err := deps.store.ListReviews(ctx, b.IssueSlug)
		if err == nil && len(rows) > 0 {
			withHistory = append(withHistory, b)
		}
	}

	if len(withHistory) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No review history found.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select issue to view review history:", withHistory, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	return runReviewStatus(ctx, deps, picked.IssueSlug)
}

func runReviewStatus(ctx context.Context, deps reviewDeps, issueSlug string) error {
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}

	rows, err := deps.store.ListReviews(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("list reviews: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintf(deps.client.IO().Out, "No review history for issue %q.\n", issueSlug)
		return nil
	}

	// Reconcile the latest row from the ref (authoritative source).
	// This catches status changes (e.g. rejection) made on another machine.
	ref, _ := reviewpkg.Load(ctx, deps.client, issueSlug)
	if ref != nil {
		for _, w := range ref.Warnings {
			fmt.Fprintln(deps.client.IO().Err, w)
		}
		latest := &rows[0] // ListReviews returns the newest round first
		if store.ReviewStatus(ref.Status) != latest.Status {
			_ = deps.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatus(ref.Status), latest.HasCommits)
			latest.Status = store.ReviewStatus(ref.Status)
		}
		if ref.Reviewer != "" && latest.Reviewer == "" {
			_ = deps.store.UpdateReviewerIdentity(ctx, latest.ID, ref.Reviewer)
			latest.Reviewer = ref.Reviewer
		}
	}

	fmt.Fprintf(deps.client.IO().Out, "Review history for issue %q:\n", issueSlug)
	for _, row := range rows {
		resolved := "pending"
		if row.ResolvedAt != nil {
			resolved = row.ResolvedAt.Format("2006-01-02 15:04")
		}
		commits := ""
		if row.HasCommits {
			commits = " [reviewer pushed commits]"
		}
		reviewer := row.Reviewer
		if reviewer == "" {
			reviewer = "(awaiting)"
		}
		fmt.Fprintf(deps.client.IO().Out, "  Round %-2d  %-20s  reviewer: %-30s  opened: %s  resolved: %s%s\n",
			row.Round, row.Status, reviewer,
			row.CreatedAt.Format("2006-01-02 15:04"), resolved, commits)
	}

	// The state only carries the current round's reason; older ones stay in
	// the chain's reject ops.
	if ref != nil && ref.Comment != "" && store.ReviewStatus(ref.Status) == store.ReviewStatusChangesRequested {
		fmt.Fprintf(deps.client.IO().Out, "\nRound %d reason:\n%s\n", ref.Round, indentLines(ref.Comment))
	}

	if ref != nil && len(ref.Approvals) > 0 {
		fmt.Fprintf(deps.client.IO().Out, "\nRound %d approvals:\n", ref.Round)
		for _, a := range ref.Approvals {
			// author is what the op declares; for a verified approval, also
			// show who git says signed it.
			state := reviewpkg.SignatureState(ctx, deps.client, a.Commit)
			if state == reviewpkg.SigVerified {
				if signer, _ := deps.client.CommitSigner(ctx, a.Commit); signer != "" {
					state += ", signed by " + signer
				}
			}
			fmt.Fprintf(deps.client.IO().Out, "  %s  %s\n", a.Author, state)
		}
	}

	return nil
}
