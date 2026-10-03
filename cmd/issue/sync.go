package issue

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch, merge and push the issues stored in the repository",
		Long: `Fetch refs/zf/issues/* from the remote, merge issues that were changed on
both sides, and push the issues the remote does not have yet. Without a remote
there is nothing to do.`,
		Args: cobra.NoArgs,
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return i.syncRunE(cmd)
	}

	return cmd
}

func (i Issue) syncRunE(cmd *cobra.Command) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runSync(cmd.Context(), client)
}

func runSync(ctx context.Context, client *git.Client) error {
	res, err := issuepkg.Sync(ctx, client)
	if err != nil {
		return fmt.Errorf("sync issues: %w", err)
	}

	fmt.Fprintf(client.IO().Out, "Issues synced: %d merged, %d pushed.\n", res.Merged, res.Pushed)

	for _, line := range res.Failed {
		fmt.Fprintf(client.IO().Err, "WARN: not pushed: %s\n", line)
	}
	if len(res.Failed) > 0 {
		return fmt.Errorf("%d issue(s) could not be pushed", len(res.Failed))
	}

	return nil
}
