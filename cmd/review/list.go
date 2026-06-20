package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all issues currently in review or approved",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewList(ctx, deps)
		},
	}
}

func runReviewList(ctx context.Context, deps reviewDeps) error {
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
	}

	branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	printed := 0
	for _, b := range branches {
		latest, err := deps.store.GetLatestReview(ctx, b.IssueSlug)
		if err != nil || latest == nil {
			continue
		}
		if latest.Status != store.ReviewStatusInReview && latest.Status != store.ReviewStatusApproved {
			continue
		}

		fmt.Fprintf(deps.client.IO().Out, "%-12s  %-30s  round %-2d  %s\n",
			b.IssueSlug, b.BranchName, latest.Round, latest.Status)
		printed++
	}

	if printed == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently in review.")
	}

	return nil
}
