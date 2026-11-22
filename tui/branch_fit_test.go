package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/piprim/git-zf/branch"
)

func TestBranchTable_FitsHeight(t *testing.T) {
	const height = 10
	size := func(h int) tea.Msg { return tea.WindowSizeMsg{Width: 130, Height: h} }

	// table is thirty local branches, with msgs sent.
	table := func(t *testing.T, msgs ...tea.Msg) tea.Model {
		t.Helper()
		now := time.Now()
		rows := make([]branch.Row, 30)
		for i := range rows {
			rows[i] = branch.Row{BranchName: fmt.Sprintf("branch-%02d", i), Plain: true, LastCommit: now}
		}
		m := BranchTableModel(rows, BranchTableOptions{Remote: "origin", Types: []string{"feat", "fix"}})
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
		fits(t, table(t, size(height)), "[/]-Filter")
	})

	t.Run("active filters line", func(t *testing.T) {
		fits(t, table(t, size(height), runeKey('R')), "remotes: on")
	})

	t.Run("filtering", func(t *testing.T) {
		fits(t, table(t, size(height), runeKey('/')), "(esc: clear  enter: confirm)")
	})

	t.Run("S picker open", func(t *testing.T) {
		fits(t, table(t, size(height), runeKey('S')), "Statuses")
	})

	t.Run("resize with the S picker open", func(t *testing.T) {
		fits(t, table(t, size(height), runeKey('S'), size(height)), "Statuses")
	})

	t.Run("the pickers take the terminal's width", func(t *testing.T) {
		m, ok := table(t, tea.WindowSizeMsg{Width: 90, Height: height}).(*branchTableModel)
		if !ok || m.width != 90 {
			t.Errorf("width=%d, want 90", m.width)
		}
	})

	t.Run("armed D notice", func(t *testing.T) {
		m := table(t, size(height), runeKey('D'))
		fits(t, m, m.(*branchTableModel).notice)
		if m.(*branchTableModel).notice == "" {
			t.Error("D armed nothing: the test has no notice to check")
		}
	})

	t.Run("no branch matches", func(t *testing.T) {
		m := typeText(table(t, size(height), runeKey('/')), "zzz")
		fits(t, m, "No branch matches.")
	})

	t.Run("a taller terminal shows more rows", func(t *testing.T) {
		view := ansi.Strip(table(t, size(40)).View())
		if h := lipgloss.Height(view); h > 40 || !strings.Contains(view, "branch-25") {
			t.Errorf("view is %d lines, want at most 40 with the 26th branch:\n%s", h, view)
		}
	})

	t.Run("no size yet keeps twenty rows", func(t *testing.T) {
		if h := lipgloss.Height(table(t).(*branchTableModel).table.View()); h != branchTableHeight {
			t.Errorf("table is %d lines, want %d", h, branchTableHeight)
		}
	})
}
