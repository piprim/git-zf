package review

import (
	"context"
	"fmt"

	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/spf13/cobra"
)

func (r Review) getListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all issues currently in review or approved",
		RunE:  withDeps(r.appConfig, runReviewList),
	}
}

func runReviewList(ctx context.Context, deps reviewDeps) error {
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}

	// Read from the review chains: works on a fresh clone that never ran git
	// zf issue start.
	states, warnings, err := reviewpkg.List(ctx, deps.client)
	if err != nil {
		return fmt.Errorf("list review refs: %w", err)
	}
	for _, w := range warnings {
		fmt.Fprintln(deps.client.IO().Err, w)
	}

	printed := 0
	for _, st := range states {
		if st.Closed || (st.Status != reviewpkg.StatusInReview && st.Status != reviewpkg.StatusApproved) {
			continue
		}
		fmt.Fprintf(deps.client.IO().Out, "%-12s  round %-2d  %s\n", st.Slug, st.Round, st.Status)
		printed++
	}

	if printed == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently in review.")
	}

	return nil
}
