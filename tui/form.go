package tui

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
)

// Form builds a huh form that esc leaves like ctrl+c does: the run returns
// huh.ErrUserAborted and the caller goes back to where it came from. huh's
// default Quit binding is ctrl+c alone.
func Form(groups ...*huh.Group) *huh.Form {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))

	return huh.NewForm(groups...).WithKeyMap(km)
}
