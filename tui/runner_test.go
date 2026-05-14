package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

func newTestRunner() *FormRunner {
	form := huh.NewForm(huh.NewGroup(huh.NewInput().Title("test")))
	return &FormRunner{form: form}
}

func TestFormRunner(t *testing.T) {
	t.Parallel()

	t.Run("WantHistory is false before any key press", func(t *testing.T) {
		t.Parallel()

		if newTestRunner().WantHistory() {
			t.Error("WantHistory() = true, want false before any key press")
		}
	})

	t.Run("ctrl+r sets WantHistory and returns quit cmd", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner()
		model, cmd := r.Update(tea.KeyMsg{Type: tea.KeyCtrlR})

		if !r.WantHistory() {
			t.Error("WantHistory() = false, want true after ctrl+r")
		}
		if model != r {
			t.Error("Update must return the same *FormRunner pointer")
		}
		if cmd == nil {
			t.Error("Update must return a non-nil cmd (tea.Quit) on ctrl+r")
		}
	})

	t.Run("WantHistory state does not leak between independent runner instances", func(t *testing.T) {
		t.Parallel()

		r1 := newTestRunner()
		r2 := newTestRunner()

		r1.Update(tea.KeyMsg{Type: tea.KeyCtrlR})

		if r2.WantHistory() {
			t.Error("r2.WantHistory() = true — mutation leaked between runners")
		}
	})

	t.Run("non-history key leaves WantHistory false", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner()
		r.Update(tea.KeyMsg{Type: tea.KeyEsc})

		if r.WantHistory() {
			t.Error("WantHistory() = true after esc, want false")
		}
	})
}
