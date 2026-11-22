package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestClampHeight(t *testing.T) {
	// with makes sess the session in charge for the subtest.
	with := func(t *testing.T, sess *session) {
		t.Helper()
		old := screen
		screen = sess
		t.Cleanup(func() { screen = old })
	}
	split := func(t *testing.T) *session {
		t.Helper()
		ts := newTestSession(true, 40) // git-zf has 20 rows, the output 20
		ts.screen(t)

		return ts.session
	}

	t.Run("no split: unchanged", func(t *testing.T) {
		with(t, newTestSession(false, 40).session)
		if got := clampHeight(nil, tea.WindowSizeMsg{Width: 80, Height: 40}); got != (tea.WindowSizeMsg{Width: 80, Height: 40}) {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("split, taller than the output band", func(t *testing.T) {
		with(t, split(t))
		// two rows are kept for the help line huh adds under a form
		if got := clampHeight(nil, tea.WindowSizeMsg{Width: 80, Height: 40}); got != (tea.WindowSizeMsg{Width: 80, Height: 18}) {
			t.Errorf("got %+v, want 80x18", got)
		}
	})

	t.Run("split, already smaller", func(t *testing.T) {
		with(t, split(t))
		if got := clampHeight(nil, tea.WindowSizeMsg{Width: 80, Height: 8}); got != (tea.WindowSizeMsg{Width: 80, Height: 8}) {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("an output band of two rows still clamps", func(t *testing.T) {
		ts := newTestSession(true, 40)
		ts.screen(t)
		ts.rows = 22 // shrunk during the action: the output has 2 rows left
		with(t, ts.session)
		if got := clampHeight(nil, tea.WindowSizeMsg{Width: 80, Height: 22}); got != (tea.WindowSizeMsg{Width: 80, Height: 1}) {
			t.Errorf("got %+v, want 80x1", got)
		}
	})

	t.Run("other messages pass", func(t *testing.T) {
		with(t, split(t))
		key := tea.KeyMsg{Type: tea.KeyEnter}
		if got, ok := clampHeight(nil, key).(tea.KeyMsg); !ok || got.Type != tea.KeyEnter {
			t.Errorf("got %#v", got)
		}
	})
}
