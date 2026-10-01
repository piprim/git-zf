package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ErrMenuUnavailable is returned by a MenuPrompter when there is no
// interactive terminal to draw the menu on (piped stdin, CI, cron).
var ErrMenuUnavailable = errors.New("menu: no interactive terminal")

// MenuPrompter presents a list of subcommands and returns the Name() of the
// one the user picked. The production implementation opens a huh form; tests
// substitute a scripted one.
type MenuPrompter interface {
	// Select shows title above one entry per sub and returns the picked
	// sub's Name(). It reports ErrMenuUnavailable when no terminal is
	// attached and huh.ErrUserAborted when the user presses Esc / ctrl+c.
	Select(ctx context.Context, title string, subs []*cobra.Command) (string, error)
}

// huhMenuPrompter is the production MenuPrompter.
type huhMenuPrompter struct{}

// NewHuhMenuPrompter returns the huh-backed MenuPrompter used by the CLI.
func NewHuhMenuPrompter() MenuPrompter { return huhMenuPrompter{} }

// Select opens the menu form. Without a terminal on stdin it reports
// ErrMenuUnavailable instead of letting huh fail on /dev/tty.
func (huhMenuPrompter) Select(ctx context.Context, title string, subs []*cobra.Command) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", ErrMenuUnavailable
	}

	var picked string
	if err := huh.NewForm(tui.MenuSelect(title, menuOptions(subs), &picked)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("menu form: %w", err)
	}

	return picked, nil
}

// RunMenu is the RunE body of a command group invoked with no subcommand: it
// shows a menu of subs and runs the picked one.
//
// The picked subcommand's RunE receives cmd — the parent, currently executing
// command — rather than itself. cobra v1.1.3 offers no SetContext, and the
// parent is the only command carrying a live Context and IO streams. Flag
// reads on the parent tolerate the missing flags (pflag reports unknown flags
// as unchanged; pushflow.ReadFlags checks Lookup first), so the subcommand
// runs on its interactive defaults, exactly as branch.runE already dispatches
// to its own subcommands.
//
// Esc / ctrl+c on the menu exits quietly; a non-interactive stdin prints the
// group's help instead, so scripts keep the pre-menu behaviour.
func RunMenu(cmd *cobra.Command, title string, subs []*cobra.Command, p MenuPrompter) error {
	picked, err := p.Select(cmd.Context(), title, subs)

	switch {
	case errors.Is(err, ErrMenuUnavailable):
		if err := cmd.Help(); err != nil {
			return fmt.Errorf("print help: %w", err)
		}

		return nil
	case errors.Is(err, huh.ErrUserAborted):
		return nil
	case err != nil:
		return fmt.Errorf("action select: %w", err)
	}

	i := slices.IndexFunc(subs, func(s *cobra.Command) bool { return s.Name() == picked })
	if i < 0 {
		return fmt.Errorf("unknown action %q", picked)
	}

	if subs[i].RunE == nil {
		return fmt.Errorf("action %q has no RunE", picked)
	}

	if err := subs[i].RunE(cmd, nil); err != nil {
		return fmt.Errorf("%s: %w", picked, err)
	}

	return nil
}

// menuOptions derives the menu entries from the subcommands themselves so
// labels and descriptions cannot drift from the CLI: label is the capitalised
// command name, description its Short, value its Name().
func menuOptions(subs []*cobra.Command) []tui.MenuOption {
	opts := make([]tui.MenuOption, len(subs))
	for i, s := range subs {
		opts[i] = tui.MenuOption{Label: tui.TitleCase(s.Name()), Desc: s.Short, Value: s.Name()}
	}

	return opts
}
