package log

import (
	"context"
	"fmt"
	"os"
	"strings"

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
L on a merge at the tip of a local branch replaces it by one commit holding its
tree (the branch stays bit-identical but linear), after fetching: it is refused
when the upstream moved, and the push that follows is offered with a lease on
what was fetched. The old tip stays in the reflog.
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

	model := tui.LogModel(lines, cfg.Cmds, current, logActions(ctx, client, load))
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		return fmt.Errorf("run log: %w", err)
	}

	return nil
}

// logActions binds the git operations of the log view to client.
func logActions(ctx context.Context, client *git.Client, load func(config.LogCmd) ([]string, error)) tui.LogActions {
	return tui.LogActions{
		Load: load,
		Show: func(hash string) (string, error) { return client.ShowCommit(ctx, hash) },
		Plan: func(hash string) (tui.Linearization, error) { return planLinearize(ctx, client, hash) },
	}
}

// planLinearize words the plan of replacing the merge hash by one commit,
// and its push when the branch has an upstream.
func planLinearize(ctx context.Context, client *git.Client, hash string) (tui.Linearization, error) {
	p, err := client.LinearizePlan(ctx, hash)
	if err != nil {
		return tui.Linearization{}, fmt.Errorf("linearize %s: %w", hash, err)
	}
	l := tui.Linearization{
		Confirm: fmt.Sprintf("Linearize %s on %s?", hash, p.Branch),
		Apply: func() (string, error) {
			commit, err := client.LinearizeMerge(ctx, &p)
			if err != nil {
				return "", fmt.Errorf("linearize %s: %w", hash, err)
			}

			return fmt.Sprintf("%s: %s → %s (the old tip is in the reflog)", p.Branch, hash, commit[:7]), nil
		},
	}
	if p.Upstream == "" {
		return l, nil
	}
	if p.Force {
		l.Confirm += fmt.Sprintf(" %s is at it too: the push will be forced.", p.Upstream)
		l.PushConfirm = fmt.Sprintf("Push %s to %s with --force-with-lease=%s:%s?",
			p.Branch, p.Remote, strings.TrimPrefix(p.RemoteRef, "refs/heads/"), p.UpstreamHash[:7])
	} else {
		l.PushConfirm = fmt.Sprintf("Push %s to %s (fast-forward)?", p.Branch, p.Remote)
	}
	l.Push = func() (string, error) {
		if err := client.PushLinearized(ctx, &p); err != nil {
			return "", fmt.Errorf("push %s: %w", p.Branch, err)
		}

		return "pushed " + p.Branch + " to " + p.Upstream, nil
	}

	return l, nil
}
