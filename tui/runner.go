package tui

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

const (
	// panelGap is the number of blank columns rendered between the form and the
	// status panel, and reserved when sizing the form.
	panelGap = 2
	// minFormWidth is the floor for the form width when the terminal is too
	// narrow to fit both the form and the panel side by side; below this the
	// panel may overflow (accepted narrow-terminal limitation).
	minFormWidth = 20
)

// FormRunner wraps a *huh.Form as a tea.Model to intercept ctrl+r before huh
// sees it, and to render an optional read-only panel to the right of the form.
type FormRunner struct {
	form        *huh.Form
	wantHistory bool
	panel       string
}

// WantHistory reports whether the user pressed ctrl+r during the form.
func (r *FormRunner) WantHistory() bool { return r.wantHistory }

// Init implements tea.Model.
func (r *FormRunner) Init() tea.Cmd { return r.form.Init() }

// Update implements tea.Model.
func (r *FormRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+r" {
		r.wantHistory = true

		return r, tea.Quit
	}

	// Reserve horizontal room for the status panel. huh sizes the form to the
	// full terminal width on a WindowSizeMsg (form.go: `if f.width == 0`), which
	// would push the joined panel off the right edge. Setting an explicit form
	// width both fits the panel on-screen and sticks, because huh skips its own
	// width resize once f.width != 0.
	if ws, ok := msg.(tea.WindowSizeMsg); ok && r.panel != "" {
		formWidth := ws.Width - lipgloss.Width(r.panel) - panelGap
		if formWidth < minFormWidth {
			formWidth = minFormWidth
		}
		r.form = r.form.WithWidth(formWidth)
		msg = tea.WindowSizeMsg{Width: formWidth, Height: ws.Height}
	}

	updated, cmd := r.form.Update(msg)
	if f, ok := updated.(*huh.Form); ok {
		r.form = f
	}

	return r, cmd
}

// View implements tea.Model. When a panel is set, it is joined to the right of
// the form; otherwise only the form is rendered.
func (r *FormRunner) View() string {
	if r.panel == "" {
		return r.form.View()
	}

	panel := lipgloss.NewStyle().MarginLeft(panelGap).Render(r.panel)

	return lipgloss.JoinHorizontal(lipgloss.Top, r.form.View(), panel)
}

// RunForm runs form inside a bubbletea program, rendering panel (when non-empty)
// to the right of the form.
// Returns (runner, nil) on normal completion or ctrl+r; call runner.WantHistory() to distinguish.
// Returns (runner, huh.ErrUserAborted) on ctrl+c / esc.
func RunForm(form *huh.Form, panel string) (*FormRunner, error) {
	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit

	r := &FormRunner{form: form, panel: panel}
	_, err := tea.NewProgram(r, tea.WithOutput(os.Stderr)).Run()

	if errors.Is(err, tea.ErrInterrupted) {
		return r, huh.ErrUserAborted
	}
	if err != nil {
		return r, fmt.Errorf("run form: %w", err)
	}

	if r.form.State == huh.StateAborted {
		return r, huh.ErrUserAborted
	}

	return r, nil
}
