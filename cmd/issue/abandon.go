package issue

import (
	"fmt"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/spf13/cobra"
)

func (i Issue) getAbandonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "abandon [<id>]",
		Short: "Close an issue without merging anything",
		Long: `Close an issue stored in the repository without merging anything: for a
duplicate or an issue that will not be worked on. The close is pushed and,
with the issue mirror on, the tracker issue is closed too.

<id> is the full ID of a repository issue or a unique prefix of at least 4
characters; without one a picker lists the repository issues. A tracker
issue that is not in the repository cannot be abandoned.

It is refused while a branch of the issue is in progress: merge it with
git zf issue close <id>, or abandon the branch with
git zf branch close <branch-name>, then abandon the issue. To close an issue
by merging its branch, use git zf issue close.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
			if err != nil {
				return fmt.Errorf("open repository: %w", err)
			}

			return runAbandon(cmd.Context(), client, huhRecordPrompter{}, args, openMirror(i.appConfig, client.IO().Err))
		},
	}
}
