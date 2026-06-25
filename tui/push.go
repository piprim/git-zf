package tui

import "github.com/charmbracelet/huh"

// PushConfirm asks whether to push a branch to the remote. summary is the
// preview line (e.g. `Push "feat-x" to origin?`). The caller seeds *confirmed
// to set the default selection.
func PushConfirm(summary string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title(summary).
			Affirmative("Push").
			Negative("Skip").
			Value(confirmed),
	)
}
