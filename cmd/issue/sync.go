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
there is nothing to do. With issue-tracker.mirror on, it then mirrors the
issues with the tracker project: imports, exports, and open/closed both ways.`,
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

	return runSync(cmd.Context(), client, openMirror(i.appConfig, client.IO().Err))
}

func runSync(ctx context.Context, client *git.Client, m *issuepkg.Mirror) error {
	res, err := issuepkg.Sync(ctx, client)
	if err != nil {
		return fmt.Errorf("sync issues: %w", err)
	}

	fmt.Fprintf(client.IO().Out, "Issues synced: %d merged, %d pushed.\n", res.Merged, res.Pushed)

	if m != nil {
		mr := reconcileIssues(ctx, client, m)
		fmt.Fprintf(client.IO().Out, "Tracker mirror: %d imported, %d exported, %d pulled, %d pushed.\n",
			mr.Imported, mr.Exported, mr.Pulled, mr.Pushed)
	}

	for _, line := range res.Failed {
		fmt.Fprintf(client.IO().Err, "WARN: not pushed: %s\n", line)
	}
	if len(res.Failed) > 0 {
		return fmt.Errorf("%d issue(s) could not be pushed", len(res.Failed))
	}

	return nil
}
