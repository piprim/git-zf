package tui

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/piprim/git-zf/git"
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
// sees it, and to render an optional read-only "Current Git Status" panel to
// the right of the form.
//
// The panel is recomputed on every render from a fixed working-tree snapshot
// (entries) and the form's live --all value (allFn), so toggling --all in the
// form re-classifies the panel in real time. reserveWidth is the widest the
// panel can be across both layouts, so the form width stays stable as the
// toggle flips. When entries is empty there is no panel.
type FormRunner struct {
	form         *huh.Form
	wantHistory  bool
	entries      []git.StatusEntry
	allFn        func() bool
	reserveWidth int
}

// WantHistory reports whether the user pressed ctrl+r during the form.
func (r *FormRunner) WantHistory() bool { return r.wantHistory }

// hasPanel reports whether a status panel should be rendered alongside the form.
func (r *FormRunner) hasPanel() bool { return len(r.entries) > 0 }

// all reads the live --all value, defaulting to false when no reader is set.
func (r *FormRunner) all() bool { return r.allFn != nil && r.allFn() }

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
	// width resize once f.width != 0. reserveWidth covers both --all layouts so
	// the form does not resize when the toggle flips.
	if ws, ok := msg.(tea.WindowSizeMsg); ok && r.hasPanel() {
		formWidth := ws.Width - r.reserveWidth - panelGap
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

// View implements tea.Model. When a panel is present it is recomputed for the
// current --all value and joined to the right of the form; otherwise only the
// form is rendered.
//
// The panel is only attached while the form is actually rendering something.
// huh returns an empty view once the form is quitting (completed or aborted);
// attaching the panel then would leave it as the program's final on-screen
// frame after the form closes.
func (r *FormRunner) View() string {
	formView := r.form.View()
	if !r.hasPanel() || formView == "" {
		return formView
	}

	panel := lipgloss.NewStyle().
		MarginLeft(panelGap).
		Render(StatusPanel(r.entries, r.all()))

	return lipgloss.JoinHorizontal(lipgloss.Top, formView, panel)
}

// RunForm runs form inside a bubbletea program. When entries is non-empty it
// renders a live "Current Git Status" panel to the right of the form, whose
// staged/unstaged split follows allFn (the form's current --all value).
// Returns (runner, nil) on normal completion or ctrl+r; call runner.WantHistory() to distinguish.
// Returns (runner, huh.ErrUserAborted) on ctrl+c / esc.
func RunForm(form *huh.Form, entries []git.StatusEntry, allFn func() bool) (*FormRunner, error) {
	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit

	r := &FormRunner{
		form:         form,
		entries:      entries,
		allFn:        allFn,
		reserveWidth: StatusPanelReserveWidth(entries),
	}
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
