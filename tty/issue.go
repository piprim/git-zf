package tty

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
	lgtable "github.com/charmbracelet/lipgloss/table"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

func RenderIssueTable(w io.Writer, rows []store.IssueRow) {
	t := lgtable.New().
		Headers("ISSUE ID", "TITLE", "BRANCH", "LOCAL STATUS", "TRACKER STATUS", "CREATED").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == lgtable.HeaderRow {
				return lipgloss.NewStyle().Bold(true)
			}

			return lipgloss.NewStyle()
		})

	for _, r := range rows {
		t.Row(
			r.IssueSlug,
			r.Title,
			pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
			pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
			pkg.TrackerStatusOrNA(r.TrackerStatus),
			pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
		)
	}

	fmt.Fprintln(w, t.Render())
}
