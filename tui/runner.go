package tui

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// FormRunner wraps a *huh.Form as a tea.Model to intercept ctrl+r before huh sees it.
type FormRunner struct {
	form        *huh.Form
	wantHistory bool
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

	updated, cmd := r.form.Update(msg)
	if f, ok := updated.(*huh.Form); ok {
		r.form = f
	}

	return r, cmd
}

// View implements tea.Model.
func (r *FormRunner) View() string { return r.form.View() }

// RunForm runs form inside a bubbletea program.
// Returns (runner, nil) on normal completion or ctrl+r; call runner.WantHistory() to distinguish.
// Returns (runner, huh.ErrUserAborted) on ctrl+c / esc.
func RunForm(form *huh.Form) (*FormRunner, error) {
	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit

	r := &FormRunner{form: form}
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
