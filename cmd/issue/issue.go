package issue

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/config"
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

	cmd.AddCommand(i.getStartCmd(), i.getIssueListCmd())

	return cmd
}

func (i Issue) runE(cmd *cobra.Command, args []string) error {
	var action string
	if err := huh.NewForm(tui.IssueActionSelect(&action)).Run(); err != nil {
		return fmt.Errorf("action select: %w", err)
	}

	switch action {
	case tui.IssueActionNameStart:
		return i.startRunE(cmd, args)
	case tui.IssueActionNameList:
		return i.issueListRunE(cmd, issueListFlags{})
	default:
		fmt.Println("Not yet implemented.")

		return nil
	}
}
