package issue

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/config"
	_ "github.com/piprim/git-zf/tracker/github"  // registers github adapter
	_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
	"github.com/piprim/git-zf/tui"
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
		RunE:  i.runE,
	}

	cmd.AddCommand(i.getStartCmd(), i.getIssueListCmd(), i.getCloseCmd())

	return cmd
}

func (i Issue) runE(cmd *cobra.Command, args []string) error {
	var action string
	if err := huh.NewForm(tui.IssueActionSelect(&action)).RunWithContext(cmd.Context()); err != nil {
		return fmt.Errorf("action select: %w", err)
	}

	switch action {
	case tui.IssueActionNameStart:
		// Interactive path: the issue root command defines no --variant flag,
		// so pass an empty variant.
		return i.startRunE(cmd, "")
	case tui.IssueActionNameList:
		return i.issueListRunE(cmd, issueListFlags{})
	case tui.IssueActionNameClose:
		return i.closeRunE(cmd, args)
	default:
		fmt.Fprintln(cmd.OutOrStdout(), "Not yet implemented.")

		return nil
	}
}
