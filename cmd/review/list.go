package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

// store is used indirectly via ReviewStatusInReview/Approved constants.

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

	// Read directly from git refs — works even when the reviewer's store is
	// empty (fresh clone that never ran git zf issue start).
	allRefs, err := deps.client.ListReviewRefs(ctx)
	if err != nil {
		return fmt.Errorf("list review refs: %w", err)
	}

	printed := 0
	for issueID, ref := range allRefs {
		if ref.Status != string(store.ReviewStatusInReview) && ref.Status != string(store.ReviewStatusApproved) {
			continue
		}
		fmt.Fprintf(deps.client.IO().Out, "%-12s  round %-2d  %s\n",
			issueID, ref.Round, ref.Status)
		printed++
	}

	if printed == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently in review.")
	}

	return nil
}
