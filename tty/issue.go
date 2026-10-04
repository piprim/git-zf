package tty

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
	lgtable "github.com/charmbracelet/lipgloss/table"
	"github.com/piprim/git-zf/store"
)

func RenderIssueTable(w io.Writer, rows []store.IssueRow) {
	includeProject := len(store.UniqueProjects(rows)) > 1

	headers := []string{"ISSUE ID"}
	if includeProject {
		headers = append(headers, "PROJECT")
	}

	headers = append(headers, "TITLE", "BRANCH", "LOCAL STATUS", "ISSUE STATUS", "CREATED")

	t := lgtable.New().
		Headers(headers...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == lgtable.HeaderRow {
				return lipgloss.NewStyle().Bold(true)
			}

			return lipgloss.NewStyle()
		})

	for i := range rows {
		t.Row(store.IssueRowCells(&rows[i], includeProject)...)
	}

	fmt.Fprintln(w, t.Render())
}
