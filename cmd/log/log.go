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
1-9 switch to the command with that key, c picks one by name; the commands and
the default are configured under [log] (see git zf config show).
Arguments after -- go to git log, e.g. a revision range or -n 100.
Without a terminal the default command's output is printed like git log.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := cmdutil.NewClientForCmd(cmd, l.appConfig)
			if err != nil {
				return fmt.Errorf("open repository: %w", err)
			}

			return runLog(cmd.Context(), client, l.appConfig.Log, args, term.IsTerminal(int(os.Stdout.Fd())))
		},
	}
}

func runLog(ctx context.Context, client *git.Client, cfg config.LogConfig, extra []string, isTTY bool) error {
	args := func(c config.LogCmd) ([]string, error) {
		a, err := c.Args()
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}

		return append(a, extra...), nil
	}
	load := func(c config.LogCmd) ([]string, error) {
		a, err := args(c)
		if err != nil {
			return nil, err
		}
		lines, err := client.Log(ctx, a...)
		if err != nil {
			return nil, fmt.Errorf("git log: %w", err)
		}

		return lines, nil
	}

	current := cfg.DefaultIndex()
	if !isTTY {
		a, err := args(cfg.Cmds[current])
		if err != nil {
			return err
		}
		if err := client.PrintLog(ctx, a...); err != nil {
			return fmt.Errorf("git log: %w", err)
		}

		return nil
	}

	lines, err := load(cfg.Cmds[current])
	if err != nil {
		return err
	}

	actions := tui.LogActions{
		Load: load,
		Show: func(hash string) (string, error) { return client.ShowCommit(ctx, hash) },
	}
	if _, err := tea.NewProgram(tui.LogModel(lines, cfg.Cmds, current, actions), tea.WithAltScreen()).Run(); err != nil {
		return fmt.Errorf("run log: %w", err)
	}

	return nil
}
