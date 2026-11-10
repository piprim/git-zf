package tty

import (
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/lipgloss"
	lgtable "github.com/charmbracelet/lipgloss/table"
	"github.com/piprim/git-zf/branch"
)

// RenderBranchRows prints the rows of the branch table (branch.TableRows) as
// the table shows them: "* " before the current branch, "<remote>/" before a
// remote-only one, blank zero dates.
func RenderBranchRows(w io.Writer, rows []branch.Row, remote string) {
	t := lgtable.New().
		Headers("BRANCH", "ISSUE", "TYPE", "STATUS", "TITLE", "CREATED", "LAST COMMIT").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == lgtable.HeaderRow {
				return lipgloss.NewStyle().Bold(true)
			}

			return lipgloss.NewStyle()
		})

	for i := range rows {
		r := &rows[i]
		name := "  "
		if r.Current {
			name = "* "
		}
		if r.RemoteOnly {
			name += remote + "/"
		}
		t.Row(name+r.BranchName, r.IssueSlug, r.Type, r.Status, r.Title, date(r.CreatedAt), date(r.LastCommit))
	}

	fmt.Fprintln(w, t.Render())
}

func date(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.Format("2006-01-02")
}
