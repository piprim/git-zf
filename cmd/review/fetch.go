package review

import (
	"context"
	"fmt"

	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/spf13/cobra"
)

func (r Review) getFetchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fetch",
		Short: "Sync review refs with the remote: fetch, merge, and push the ones it lacks",
		RunE:  withDeps(r.appConfig, runReviewFetch),
	}
}

func runReviewFetch(ctx context.Context, deps reviewDeps) error {
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		return fmt.Errorf("sync review refs: %w", err)
	}
	fmt.Fprintln(deps.client.IO().Out, "Review refs synced.")
	return nil
}
