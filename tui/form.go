package tui

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// NewForm builds a huh form in the git-zf theme: huh's default help line
// (the key hints under a form) is drawn in greys close to the background and
// is hard to read; the theme lifts them to the normal foreground.
func NewForm(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).WithTheme(theme())
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
