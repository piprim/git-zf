package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// homePrompter shows the home screen and returns what the user chose. The
// production implementation runs a bubbletea program; tests script it.
type homePrompter interface {
	// Pick shows data above entries, the cursor on the entry named cursor
	// ("" for the screen's own choice).
	Pick(ctx context.Context, data tui.HomeData, entries []tui.HomeEntry, cursor string) (tui.HomePick, error)
}

// homeDeps is what the home screen needs from the outside.
type homeDeps struct {
	prompter homePrompter
	// isTTY reports whether stdin is a terminal.
	isTTY func() bool
	// open opens the repository the command was typed in.
	open func(cmd *cobra.Command) (*git.Client, error)
}

// teaHomePrompter is the production homePrompter.
type teaHomePrompter struct{}

func (teaHomePrompter) Pick(
	ctx context.Context, data tui.HomeData, entries []tui.HomeEntry, cursor string,
) (tui.HomePick, error) {
	final, err := tea.NewProgram(tui.NewHomeModel(data, entries, cursor), tea.WithContext(ctx)).Run()
	if err != nil {
		return tui.HomePick{}, fmt.Errorf("run the home screen: %w", err)
	}

	return tui.PickedHome(final), nil
}

// newHomeDeps returns the production homeDeps.
func newHomeDeps() homeDeps {
	return homeDeps{
		prompter: teaHomePrompter{},
		isTTY:    func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		open:     func(cmd *cobra.Command) (*git.Client, error) { return cmdutil.NewClientForCmd(cmd, appConfig) },
	}
}

// runHome is the RunE body of bare `git zf`: it shows the home screen, runs
// what is picked, and shows the screen again until q. subs run as
// cmdutil.RunMenu runs them, with cmd, the executing parent.
//
// Without a terminal on stdin the help is printed and nothing else, checked
// first so a script sees the same thing in a repository or out of one. With
// a terminal but no repository there is nothing the screen could offer: one
// line says so, then the help.
//
// Every display opens the repository again: a command may have changed the
// branch, and a client that saw the remote down fails every later remote
// call, which must not outlive one refresh.
func runHome(cmd *cobra.Command, prog string, subs []*cobra.Command, d homeDeps) error {
	help := func() error {
		if err := cmd.Help(); err != nil {
			return fmt.Errorf("print help: %w", err)
		}

		return nil
	}
	if !d.isTTY() {
		return help()
	}
	client, err := d.open(cmd)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "not a git repository")

		return help()
	}

	for cursor := ""; ; {
		data, hints := gatherHome(cmd.Context(), client, prog)

		pick, err := d.prompter.Pick(cmd.Context(), data, homeEntries(subs, hints), cursor)
		if err != nil {
			return fmt.Errorf("home screen: %w", err)
		}

		switch pick.Kind {
		case tui.HomeQuit:
			return nil
		case tui.HomeRefresh:
			refreshHome(cmd.Context(), client)
		case tui.HomeRun:
			i := slices.IndexFunc(subs, func(s *cobra.Command) bool { return s.Name() == pick.Name })
			if i < 0 {
				return fmt.Errorf("unknown action %q", pick.Name)
			}
			if err := cmdutil.RunSub(cmd, subs[i]); err != nil {
				return err //nolint:wrapcheck // names the action already
			}
			cursor = pick.Name
		}

		// A close can remove the worktree the shell stands in: the command
		// did its work, there is only nothing left to show here.
		if client, err = d.open(cmd); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "this directory is no longer a git repository")

			return nil
		}
	}
}
