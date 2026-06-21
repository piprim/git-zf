package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the full review history for an issue",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewStatusInteractive(ctx, deps, newHuhReviewPrompter())
		},
	}
}

func runReviewStatusInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
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
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
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
	if ref, _, _ := deps.client.ReadReviewRef(ctx, issueSlug); ref != nil {
		latest := &rows[len(rows)-1]
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

	return nil
}
