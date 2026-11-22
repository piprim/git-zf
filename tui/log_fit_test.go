package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/piprim/git-zf/config"
)

func TestLogModel_FitsHeight(t *testing.T) {
	const height = 9 // what a band of ten rows gives its screen
	key, runeKey := logKey, logRuneKey
	size := tea.WindowSizeMsg{Width: 100, Height: height}

	// log is forty commits and six commands, with msgs sent.
	log := func(t *testing.T, msgs ...tea.Msg) tea.Model {
		t.Helper()
		lines := make([]string, 40)
		for i := range lines {
			lines[i] = fmt.Sprintf("* %07x - commit %d", 0xabc0000+i, i)
		}
		cmds := make([]config.LogCmd, 6)
		for i := range cmds {
			cmds[i] = config.LogCmd{Name: fmt.Sprintf("cmd%d", i+1), Key: i + 1}
		}
		m := LogModel(lines, cmds, 0, LogActions{
			Show: func(string) (string, error) { return strings.Repeat("patch line\n", 80), nil },
		})
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

	t.Run("list", func(t *testing.T) {
		fits(t, log(t, size), "c: pick")
	})

	t.Run("picker open", func(t *testing.T) {
		fits(t, log(t, size, runeKey('c')), "Pick command")
	})

	t.Run("picker open keeps the selected commit in view", func(t *testing.T) {
		msgs := []tea.Msg{size}
		for range 4 {
			msgs = append(msgs, key(tea.KeyDown))
		}
		fits(t, log(t, append(msgs, runeKey('c'))...), "commit 4")
	})

	t.Run("picker follows the cursor", func(t *testing.T) {
		msgs := []tea.Msg{size, runeKey('c')}
		for range 5 {
			msgs = append(msgs, key(tea.KeyDown))
		}
		fits(t, log(t, msgs...), "> cmd6")
	})

	t.Run("picker closed restores the list", func(t *testing.T) {
		m := log(t, size, runeKey('c'), key(tea.KeyEsc))
		if h := m.(*logModel).list.Height; h != height-logChrome {
			t.Errorf("list height = %d, want %d", h, height-logChrome)
		}
	})

	t.Run("commit dialog", func(t *testing.T) {
		fits(t, log(t, size, key(tea.KeyEnter)), "esc: back")
	})

	t.Run("search in the dialog", func(t *testing.T) {
		fits(t, log(t, size, key(tea.KeyEnter), runeKey('/')), "enter: search")
	})
}
