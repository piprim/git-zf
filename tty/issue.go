package tty

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
	lgtable "github.com/charmbracelet/lipgloss/table"
	"github.com/piprim/git-zf/store"
)

func RenderIssueTable(w io.Writer, rows []store.IssueRow) {
	includeProject := hasMultipleProjects(rows)

	headers := []string{"ISSUE ID"}
	if includeProject {
		headers = append(headers, "PROJECT")
	}

	headers = append(headers, "TITLE", "BRANCH", "LOCAL STATUS", "TRACKER STATUS", "CREATED")

	t := lgtable.New().
		Headers(headers...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == lgtable.HeaderRow {
				return lipgloss.NewStyle().Bold(true)
			}

			return lipgloss.NewStyle()
		})

	for _, r := range rows {
		cells := []string{r.IssueSlug}
		if includeProject {
			cells = append(cells, r.Project)
		}

		cells = append(cells,
			r.Title,
			store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
			store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
			store.TrackerStatusOrNA(r.TrackerStatus),
			store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
		)

		t.Row(cells...)
	}

	fmt.Fprintln(w, t.Render())
}

func hasMultipleProjects(rows []store.IssueRow) bool {
	first := ""
	seen := false

	for _, r := range rows {
		if r.Project == "" {
			continue
		}
		if !seen {
			first = r.Project
			seen = true

			continue
		}
		if r.Project != first {
			return true
		}
	}

	return false
}
