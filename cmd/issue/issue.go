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
		Long: `An issue is a piece of work with an identity: a ticket in the configured
tracker (Redmine, GitHub, Forgejo/Gitea), or an issue stored in the
repository itself under refs/zf/issues/ when there is no tracker. Repository
issues travel with git zf issue sync, so a fresh clone sees the same backlog,
descriptions and comments, with no account anywhere.

From the moment an issue is started it has a branch, named after it:
git zf issue start creates the branch, git zf commit pre-fills the commit
form from it, and git zf issue close merges it into the base and offers to
update the tracker. new, show, edit, comment and label work on repository
issues.

Without a subcommand the issue table opens, as git zf issue list does.`,
		// No subcommand: the list, whose table starts, shows and edits
		// an issue. The git zf menu runs this with the root command.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return i.issueListRunE(cmd, issueListFlags{})
		},
	}

	cmd.AddCommand(
		i.getStartCmd(), i.getIssueListCmd(), i.getCloseCmd(), i.getAbandonCmd(),
		i.getNewCmd(), i.getShowCmd(), i.getEditCmd(), i.getCommentCmd(), i.getLabelCmd(), i.getSyncCmd(),
		review.TrackCmd(i.appConfig), // CLI-only here; the review menu offers it
	)

	return cmd
}
