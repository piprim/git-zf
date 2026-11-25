package review

import (
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

// Review is the `git zf review` command group.
type Review struct {
	appConfig *config.AppConfig
}

// New creates a Review command group.
func New(appConfig *config.AppConfig) Review {
	return Review{appConfig: appConfig}
}

// GetRootCmd returns the `review` cobra command with all subcommands
// registered. Invoked without a subcommand it opens a menu of the user-facing
// actions (see menuSubs); the hidden guard commands are CLI-only.
func (r Review) GetRootCmd() *cobra.Command {
	menu := r.menuSubs()

	cmd := &cobra.Command{
		Use:   "review",
		Short: "Manage the code review lifecycle for an issue branch",
		Long: `Peer-to-peer code review with no server-side component. The state of a
review lives in the repository, under refs/zf/reviews/<ID>, one commit
per action, pushed to and fetched from the remote. Nothing about a review is
kept anywhere else, and actions taken on two machines at once are merged,
never overwritten.

A review round:

  1. Developer:  git zf review request   records the branch and the commit
                                         submitted
  2. Reviewer:   git fetch && git zf review start
                                         checks out that branch, brought to
                                         the remote one
  3. Reviewer:   git zf review approve   approves the commit the branch is on
             or  git zf review reject    changes requested
  4. Developer:  pushes fixes and requests again: the round counter goes up

There is one branch and no lock: the reviewer commits and pushes on the
feature branch like the developer. git zf issue close merges it only when its
tip is exactly the commit approved; any later commit needs a new round.

Without a subcommand a menu offers these actions.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmdutil.RunMenu(cmd, "Review action:", menu, cmdutil.NewHuhMenuPrompter())
		},
	}

	cmd.AddCommand(menu...)
	cmd.AddCommand(r.getGuardCmd(), r.getGuardCommitCmd())

	return cmd
}

// menuSubs builds the user-facing review subcommands in workflow order: the
// developer's request first, then the reviewer's decisions, then the
// read-only and maintenance actions. This order is both the menu and the
// registration order.
func (r Review) menuSubs() []*cobra.Command {
	return []*cobra.Command{
		r.getRequestCmd(),
		r.getStartCmd(),
		r.getApproveCmd(),
		r.getRejectCmd(),
		r.getListCmd(),
		r.getStatusCmd(),
		r.getFetchCmd(),
		r.getSyncCmd(),
		TrackCmd(r.appConfig),
	}
}
