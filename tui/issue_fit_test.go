package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/piprim/git-zf/issue"
)

func TestIssueTable_FitsHeight(t *testing.T) {
	const height = 10
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	size := func(h int) tea.Msg { return tea.WindowSizeMsg{Width: 140, Height: h} }

	// table is thirty open issues spread over projects, with msgs sent.
	table := func(t *testing.T, projects int, msgs ...tea.Msg) tea.Model {
		t.Helper()
		rows := make([]issue.Row, 30)
		for i := range rows {
			rows[i] = issue.Row{IssueSlug: fmt.Sprint(100 + i), Title: fmt.Sprintf("Issue %d", i), State: issue.StateOpen}
			if projects > 0 {
				rows[i].Project = fmt.Sprintf("proj%d", i%projects)
			}
		}
		actions := IssueActions{Show: func(string) (string, error) { return strings.Repeat("body\n", 60), nil }, Start: true}
		m, err := IssueTableModel(rows, "all", actions)
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range msgs {
			m, _ = m.Update(msg)
		}

		return m
	}
	// fits fails when the view is taller than height or lost want.
	fits := func(t *testing.T, m tea.Model, want string) {
		t.Helper()
		view := ansi.Strip(m.View())
		if h := lipgloss.Height(view); h > height {
			t.Errorf("view is %d lines, want at most %d:\n%s", h, height, view)
		}
		if !strings.Contains(view, want) {
			t.Errorf("view lost %q:\n%s", want, view)
		}
	}

	t.Run("plain", func(t *testing.T) {
		fits(t, table(t, 0, size(height)), "[/]-Filter")
	})

	t.Run("filtering", func(t *testing.T) {
		fits(t, table(t, 0, size(height), runeKey('/')), "(esc: clear  enter: confirm)")
	})

	t.Run("with a notice", func(t *testing.T) {
		fits(t, table(t, 0, size(height), runeKey('a')), "a again abandons 100")
	})

	t.Run("row menu open", func(t *testing.T) {
		fits(t, table(t, 0, size(height), key(tea.KeyEnter)), "Issue 100")
	})

	t.Run("resize with the row menu open", func(t *testing.T) {
		fits(t, table(t, 0, size(height), key(tea.KeyEnter), size(height)), "Issue 100")
	})

	t.Run("project picker open", func(t *testing.T) {
		fits(t, table(t, 2, size(height), runeKey('p')), "Pick project")
	})

	t.Run("project picker with eight projects follows the cursor", func(t *testing.T) {
		msgs := []tea.Msg{size(height), runeKey('p')}
		for range 7 {
			msgs = append(msgs, key(tea.KeyDown))
		}
		fits(t, table(t, 8, msgs...), "> proj6")
	})

	t.Run("show dialog", func(t *testing.T) {
		m := table(t, 0, size(height))
		m.(*issueTableModel).openDialog()
		m, _ = m.Update(key(tea.KeyDown)) // any message: the table sizes itself after each
		fits(t, m, "esc: back")
	})

	t.Run("a taller terminal shows more rows", func(t *testing.T) {
		view := ansi.Strip(table(t, 0, size(40)).View())
		if h := lipgloss.Height(view); h > 40 || !strings.Contains(view, "Issue 25") {
			t.Errorf("view is %d lines, want at most 40 with the 26th issue:\n%s", h, view)
		}
	})

	t.Run("no size yet keeps twenty rows", func(t *testing.T) {
		if h := lipgloss.Height(table(t, 0).(*issueTableModel).table.View()); h != issueTableHeight {
			t.Errorf("table is %d lines, want %d", h, issueTableHeight)
		}
	})
}

func TestRenderPicker(t *testing.T) {
	opts := []string{"a", "b", "c", "d", "e", "f"}

	t.Run("no budget shows every option", func(t *testing.T) {
		if h := lipgloss.Height(renderPicker("Pick", 0, opts, 0)); h != 7 {
			t.Errorf("%d lines, want 7", h)
		}
	})

	for _, cursor := range []int{0, 3, 5} {
		t.Run(fmt.Sprintf("a budget of four lines keeps option %d in view", cursor), func(t *testing.T) {
			got := ansi.Strip(renderPicker("Pick", cursor, opts, 4))
			if h := lipgloss.Height(got); h != 4 || !strings.Contains(got, "> "+opts[cursor]) || !strings.HasPrefix(got, "Pick") {
				t.Errorf("%d lines:\n%s", h, got)
			}
		})
	}

	t.Run("a budget too small still shows the option under the cursor", func(t *testing.T) {
		if got := ansi.Strip(renderPicker("Pick", 2, opts, 1)); !strings.Contains(got, "> c") {
			t.Errorf("got:\n%s", got)
		}
	})
}
