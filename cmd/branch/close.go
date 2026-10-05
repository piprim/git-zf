package branch

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	"github.com/spf13/cobra"
)

func (b Branch) closeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close <branch-name>",
		Short: "Record one tracked branch as closed, without merging or deleting it",
		Long: `Record the named branch as closed (abandoned) on its chain and push that
record. Nothing is merged and no git branch is deleted, here or on the remote:
to merge an issue branch, use 'git zf issue close'.

The branch does not have to exist in this clone, and it does not matter who
started it. Only a branch in progress can be closed.

To undo, check the branch out and run 'git zf issue track'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cmdutil.NewClientForCmd(cmd, b.appConfig)
			if err != nil {
				return err //nolint:wrapcheck // already names the cause
			}

			return runCloseBranch(cmd.Context(), c, args[0])
		},
	}
}

// runCloseBranch records branchName as closed on its chain and pushes it. A
// branch that is already merged or closed is left as it is.
func runCloseBranch(ctx context.Context, client *git.Client, branchName string) error {
	// Another clone may have merged or closed the branch already.
	if err := branch.Fetch(ctx, client); err != nil {
		fmt.Fprintf(client.IO().Err, "warning: fetch branch refs: %v\n", err)
	}

	st, e, err := branch.Find(ctx, client, branchName)
	if err != nil {
		return fmt.Errorf("read branch %q: %w", branchName, err)
	}
	if e == nil {
		return fmt.Errorf("branch %q is not tracked by git-zf (see 'git zf branch list')", branchName)
	}

	if e.Status != branch.StatusInProgress {
		fmt.Fprintf(client.IO().Out, "Branch %q is already %s.\n", branchName, e.Status)

		return nil
	}

	if err := branch.SetStatus(ctx, client, st.Slug, branchName, branch.StatusClosed); err != nil {
		return fmt.Errorf("close branch %q: %w", branchName, err)
	}
	if err := branch.Push(ctx, client, st.Slug); err != nil {
		fmt.Fprintf(client.IO().Err, "warning: push branch ref: %v\n", err)
	}

	fmt.Fprintf(client.IO().Out,
		"Branch %q recorded as closed (started by %s). The git branch itself is untouched.\n",
		branchName, e.Author)

	return nil
}
