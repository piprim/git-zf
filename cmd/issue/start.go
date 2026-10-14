package issue

import (
	"fmt"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [<id>]",
		Short: "Start work on an issue (create branch)",
		Long: `Start the issue <id>: a properly named branch is created and checked
out from the default base branch. <id> is the full ID of an issue stored in
the repository, a unique prefix of at least 4 characters, or the number of
the tracker issue. Without <id> the issue list opens, as ` + "`git zf issue`" + `
does: s starts the issue under the cursor, n files a new one. The branch is
recorded in the repository (refs/zf/branches/<issue>) and pushed, so every
clone sees it.`,
		Args: cobra.MaximumNArgs(1),
	}

	cmd.Flags().String("variant", "",
		"create a parallel branch for the same issue (e.g. --variant=spike)")
	cmd.Flags().String("parent", "",
		"parent issue slug — creates this as a sub-task branching from the parent integration branch")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// StringFlag, not GetString: the issue menu runs this with the root
		// command, which defines neither flag.
		return i.startRunE(cmd, args, cmdutil.StringFlag(cmd, "variant"), cmdutil.StringFlag(cmd, "parent"))
	}

	return cmd
}

// startRunE starts the issue named by args, or opens the list when there is
// none: the list does no sync, `s` on a row does the start. variant and
// parentSlug carry the --variant and --parent flag values, "" when run from
// the issue menu.
func (i Issue) startRunE(cmd *cobra.Command, args []string, variant, parentSlug string) error {
	flags := issuepkg.IssueStartFlags{Tracker: true, Variant: variant, ParentIssueSlug: parentSlug}
	if len(args) == 0 {
		return i.issueListRunE(cmd, issueListFlags{start: flags})
	}

	deps, err := issueflow.BuildStartDeps(cmd, i.appConfig, flags)
	if err != nil {
		return err
	}

	if err := issueflow.StartID(cmd.Context(), deps, issueflow.NewHuhStartPrompter(), args[0]); err != nil {
		return fmt.Errorf("start issue %s: %w", args[0], err)
	}

	return nil
}
