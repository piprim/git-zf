package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
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

func TestFormRunnerView(t *testing.T) {
	t.Parallel()

	t.Run("View without a panel renders only the form", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner() // panel is the zero value ""
		if r.View() != r.form.View() {
			t.Error("View() should equal form.View() when panel is empty")
		}
	})

	t.Run("View with a panel includes the panel text", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner()
		r.panel = "PANEL-MARKER"
		if !strings.Contains(r.View(), "PANEL-MARKER") {
			t.Errorf("View() should contain the panel text; got:\n%s", r.View())
		}
	})

	t.Run("WindowSizeMsg reserves room so the panel stays on-screen", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner()
		rawPanel := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Render("Current Git Status\n\nmodified: plop")
		r.panel = rawPanel
		const total = 120

		r.Update(tea.WindowSizeMsg{Width: total, Height: 30})

		panelW := lipgloss.Width(rawPanel)

		// The form must be sized to leave room for the panel + gap...
		if formW := lipgloss.Width(r.form.View()); formW > total-panelW-panelGap {
			t.Errorf("form width = %d, want <= %d (must reserve %d cols for panel+gap)",
				formW, total-panelW-panelGap, panelW+panelGap)
		}
		// ...so the combined side-by-side view fits within the terminal width.
		if w := lipgloss.Width(r.View()); w > total {
			t.Errorf("combined view width = %d, want <= %d (panel would be off-screen)", w, total)
		}
	})
}
