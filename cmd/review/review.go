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
