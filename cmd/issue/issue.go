package issue

import (
	"github.com/piprim/git-zf/cmd/review"
	"github.com/piprim/git-zf/config"
	_ "github.com/piprim/git-zf/tracker/forgejo" // registers forgejo + gitea adapters
	_ "github.com/piprim/git-zf/tracker/github"  // registers github adapter
	_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
	"github.com/spf13/cobra"
)

type Issue struct {
	appConfig *config.AppConfig
}

func New(appConfig *config.AppConfig) Issue {
	return Issue{appConfig: appConfig}
}

func (i Issue) GetRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Manage issues",
		// No subcommand: the list, whose table starts, shows and edits
		// an issue. The git zf menu runs this with the root command.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return i.issueListRunE(cmd, issueListFlags{})
		},
	}

	cmd.AddCommand(
		i.getStartCmd(), i.getIssueListCmd(), i.getCloseCmd(),
		i.getNewCmd(), i.getShowCmd(), i.getEditCmd(), i.getCommentCmd(), i.getLabelCmd(), i.getSyncCmd(),
		review.TrackCmd(i.appConfig), // CLI-only here; the review menu offers it
	)

	return cmd
}
