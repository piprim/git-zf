package issue

import (
	"fmt"

	"github.com/piprim/git-zf/cmd/issueflow"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start work on an issue (create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-zf.db.`,
	}

	cmd.Flags().String("variant", "",
		"create a parallel branch for the same issue (e.g. --variant=spike)")
	cmd.Flags().String("parent", "",
		"parent issue slug — creates this as a sub-task branching from the parent integration branch")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		variant, err := cmd.Flags().GetString("variant")
		if err != nil {
			return fmt.Errorf("read --variant flag: %w", err)
		}

		parent, err := cmd.Flags().GetString("parent")
		if err != nil {
			return fmt.Errorf("read --parent flag: %w", err)
		}

		return i.startRunE(cmd, variant, parent)
	}

	return cmd
}

// startRunE drives the tracker-first issue-start flow. variant carries the
// --variant flag value; the interactive dispatcher (runE) passes "" because
// the issue root command defines no such flag.
func (i Issue) startRunE(cmd *cobra.Command, variant, parentSlug string) error {
	flags := issuepkg.IssueStartFlags{TrackerFirst: true, Variant: variant, ParentIssueSlug: parentSlug}

	deps, err := issueflow.BuildStartDeps(cmd, i.appConfig, flags)
	if err != nil {
		return err
	}

	return issueflow.RunIssueStart(cmd.Context(), deps, issueflow.NewHuhStartPrompter())
}
