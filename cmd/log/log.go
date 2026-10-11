package log

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type Log struct {
	appConfig *config.AppConfig
}

func New(appConfig *config.AppConfig) Log {
	return Log{appConfig: appConfig}
}

func (l Log) GetRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log [-- <git log arguments>]",
		Short: "Browse the commit graph; enter shows a commit",
		Long: `Browse the commit graph of every branch and tag, without git-zf's own refs.
Enter opens the commit under the cursor with its stat and patch; esc returns.
Arguments after -- go to git log, e.g. a revision range or -n 100.
Without a terminal the graph is printed like git log.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := cmdutil.NewClientForCmd(cmd, l.appConfig)
			if err != nil {
				return fmt.Errorf("open repository: %w", err)
			}

			return runLog(cmd.Context(), client, args, term.IsTerminal(int(os.Stdout.Fd())))
		},
	}
}

func runLog(ctx context.Context, client *git.Client, extra []string, isTTY bool) error {
	if !isTTY {
		if err := client.PrintLog(ctx, extra...); err != nil {
			return fmt.Errorf("git log: %w", err)
		}

		return nil
	}

	lines, err := client.LogGraph(ctx, extra...)
	if err != nil {
		return fmt.Errorf("git log: %w", err)
	}
	if len(lines) == 1 && lines[0] == "" {
		fmt.Fprintln(client.IO().Out, "No commits found.")

		return nil
	}

	m := tui.LogModel(lines, func(hash string) (string, error) { return client.ShowCommit(ctx, hash) })
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		return fmt.Errorf("run log: %w", err)
	}

	return nil
}
