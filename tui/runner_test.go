package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/piprim/git-zf/git"
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

	t.Run("View without entries renders only the form", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner() // entries is nil → no panel
		if r.View() != r.form.View() {
			t.Error("View() should equal form.View() when there is no panel")
		}
	})

	t.Run("View with entries includes the panel content", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner()
		r.entries = []git.StatusEntry{{XY: " M", Path: "PANEL-MARKER"}}
		r.allFn = func() bool { return false }
		if !strings.Contains(r.View(), "PANEL-MARKER") {
			t.Errorf("View() should contain the panel path; got:\n%s", r.View())
		}
	})

	t.Run("View reflects the live --all value", func(t *testing.T) {
		t.Parallel()

		all := false
		r := newTestRunner()
		r.entries = []git.StatusEntry{{XY: " M", Path: "plop"}}
		r.allFn = func() bool { return all }

		if !strings.Contains(r.View(), "Changes not staged for commit:") {
			t.Errorf("all=false: expected a 'not staged' section; got:\n%s", r.View())
		}

		all = true
		if strings.Contains(r.View(), "Changes not staged for commit:") {
			t.Errorf("all=true: 'not staged' section should be gone; got:\n%s", r.View())
		}
		if !strings.Contains(r.View(), "Changes to be committed:") {
			t.Errorf("all=true: change should move under 'to be committed'; got:\n%s", r.View())
		}
	})

	t.Run("no panel once the form is done (quitting/aborted)", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner()
		r.entries = []git.StatusEntry{{XY: " M", Path: "plop"}}
		r.allFn = func() bool { return false }

		// Precondition: while the form is active, the panel is attached.
		if !strings.Contains(r.View(), "Current Git Status") {
			t.Fatalf("active form should show the panel; got:\n%s", r.View())
		}

		// ctrl+c aborts the huh form → its View() becomes empty.
		r.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

		if got := r.View(); got != r.form.View() {
			t.Errorf("after the form is done, View() must be the (empty) form view, not the panel; got:\n%q", got)
		}
		if strings.Contains(r.View(), "Current Git Status") {
			t.Errorf("panel must not linger after the form closes; got:\n%s", r.View())
		}
	})

	t.Run("WindowSizeMsg reserves room so the panel stays on-screen", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner()
		r.entries = []git.StatusEntry{{XY: " M", Path: "some/longish/path/to/plop.go"}}
		r.allFn = func() bool { return false }
		r.reserveWidth = StatusPanelReserveWidth(r.entries)
		const total = 120

		r.Update(tea.WindowSizeMsg{Width: total, Height: 30})

		// The form must be sized to leave room for the widest panel + gap...
		if formW := lipgloss.Width(r.form.View()); formW > total-r.reserveWidth-panelGap {
			t.Errorf("form width = %d, want <= %d (must reserve %d cols for panel+gap)",
				formW, total-r.reserveWidth-panelGap, r.reserveWidth+panelGap)
		}
		// ...so the combined side-by-side view fits within the terminal width.
		if w := lipgloss.Width(r.View()); w > total {
			t.Errorf("combined view width = %d, want <= %d (panel would be off-screen)", w, total)
		}
	})
}
