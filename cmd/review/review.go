package review

import (
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

// GetRootCmd returns the `review` cobra command with all subcommands registered.
func (r Review) GetRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Manage the code review lifecycle for an issue branch",
	}

	cmd.AddCommand(
		r.getRequestCmd(),
		r.getStartCmd(),
		r.getApproveCmd(),
		r.getRejectCmd(),
		r.getListCmd(),
		r.getStatusCmd(),
		r.getFetchCmd(),
		r.getSyncCmd(),
		r.getGuardCmd(),
		r.getGuardCommitCmd(),
		TrackCmd(r.appConfig),
	)

	return cmd
}
