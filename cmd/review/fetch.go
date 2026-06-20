package review

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func (r Review) getFetchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fetch",
		Short: "Fetch all review refs from remote and reconcile local store",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewFetch(ctx, deps)
		},
	}
}

func runReviewFetch(ctx context.Context, deps reviewDeps) error {
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		return fmt.Errorf("fetch review refs: %w", err)
	}
	fmt.Fprintln(deps.client.IO().Out, "Review refs fetched.")
	return nil
}
