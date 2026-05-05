package tty

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
	lgtable "github.com/charmbracelet/lipgloss/table"
	"github.com/piprim/git-zf/store"
)

func RenderBranchTable(w io.Writer, rows []store.BranchRow) {
	t := lgtable.New().
		Headers("ISSUE ID", "TITLE", "BRANCH", "TYPE", "STATUS", "CREATED").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == lgtable.HeaderRow {
				return lipgloss.NewStyle().Bold(true)
			}

			return lipgloss.NewStyle()
		})

	for i := range rows {
		t.Row(
			rows[i].IssueSlug,
			rows[i].Title,
			rows[i].BranchName,
			rows[i].Type,
			string(rows[i].Status),
			rows[i].CreatedAt.Format("2006-01-02"),
		)
	}

	fmt.Fprintln(w, t.Render())
}
