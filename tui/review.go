package tui

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/store"
)

// ReviewBranchPicker presents a branch list for review commands (request, approve,
// reject, sync). The branch whose IssueSlug matches currentSlug is pre-selected;
// falls back to the first row.
func ReviewBranchPicker(title string, rows []store.BranchRow, currentSlug string, selected *store.BranchRow) *huh.Group {
	opts := make([]huh.Option[store.BranchRow], len(rows))
	for i := range rows {
		label := fmt.Sprintf("[%s] %s (%s)", rows[i].IssueSlug, rows[i].Title, rows[i].BranchName)
		opts[i] = huh.NewOption(label, rows[i])
	}

	*selected = rows[0]
	for i := range rows {
		if rows[i].IssueSlug == currentSlug {
			*selected = rows[i]
			break
		}
	}

	return huh.NewGroup(
		huh.NewSelect[store.BranchRow]().
			Title(title).
			Options(opts...).
			Value(selected),
	)
}

// ReviewIssueStartPicker presents a list of issue slugs available to start reviewing.
// The first slug is pre-selected.
func ReviewIssueStartPicker(slugs []string, selected *string) *huh.Group {
	opts := make([]huh.Option[string], len(slugs))
	for i, s := range slugs {
		opts[i] = huh.NewOption(s, s)
	}
	*selected = slugs[0]

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Select issue to review:").
			Options(opts...).
			Value(selected),
	)
}
