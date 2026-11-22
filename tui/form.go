package tui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// NewForm builds a huh form in the git-zf theme: huh's default help line
// (the key hints under a form) is drawn in greys close to the background and
// is hard to read; the theme lifts them to the normal foreground.
//
// Run on its own, the form stays in the output band of a split terminal
// (see clampHeight).
func NewForm(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).WithTheme(theme()).WithProgramOptions(tea.WithFilter(clampHeight))
}

// formMargin is what huh draws on top of the height a form is told: a
// blank line and its help.
const formMargin = 2

// clampHeight is a program filter for a form that runs between two screens:
// in a split terminal it tells the form the height of the output band, not
// the terminal's. A program taller than the band would scroll it, then lose
// track of its own lines.
func clampHeight(_ tea.Model, msg tea.Msg) tea.Msg {
	ws, ok := msg.(tea.WindowSizeMsg)
	if !ok {
		return msg
	}
	if out := OutputHeight(); out > 0 {
		ws.Height = min(ws.Height, max(1, out-formMargin))
	}

	return ws
}

// Form is NewForm with esc leaving like ctrl+c does: the run returns
// huh.ErrUserAborted and the caller goes back to where it came from. huh's
// default Quit binding is ctrl+c alone.
func Form(groups ...*huh.Group) *huh.Form {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))

	return NewForm(groups...).WithKeyMap(km)
}

func theme() *huh.Theme {
	t := huh.ThemeCharm()
	keys := lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	desc := lipgloss.AdaptiveColor{Light: "240", Dark: "246"}
	t.Help.ShortKey = t.Help.ShortKey.Foreground(keys)
	t.Help.FullKey = t.Help.FullKey.Foreground(keys)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(desc)
	t.Help.FullDesc = t.Help.FullDesc.Foreground(desc)
	t.Help.ShortSeparator = t.Help.ShortSeparator.Foreground(desc)
	t.Help.FullSeparator = t.Help.FullSeparator.Foreground(desc)
	t.Help.Ellipsis = t.Help.Ellipsis.Foreground(desc)

	return t
}
