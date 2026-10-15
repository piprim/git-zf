package tui

import (
	"fmt"

	"github.com/charmbracelet/huh"
)

// PullRequestForm edits a pull request of branchName before it is opened:
// title, body, base, then a confirm. The caller seeds the values.
func PullRequestForm(branchName string, title, body, base *string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().Title("Title:").Value(title),
		huh.NewText().Title("Body:").Value(body),
		huh.NewInput().Title("Base branch:").Value(base),
		huh.NewConfirm().Title(fmt.Sprintf("Open a pull request for %q?", branchName)).
			Affirmative("Open").Negative("Skip").Value(confirmed),
	)
}

// PullRequestConfirm is the one-question offer of the push step.
func PullRequestConfirm(branchName, base string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().Title(fmt.Sprintf("Open a pull request for %q against %q?", branchName, base)).
			Affirmative("Open").Negative("Skip").Value(confirmed),
	)
}
