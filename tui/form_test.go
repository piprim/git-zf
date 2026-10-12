package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

func TestForm(t *testing.T) {
	t.Run("esc aborts the form", func(t *testing.T) {
		var v string
		f := Form(huh.NewGroup(huh.NewInput().Value(&v)))
		f.Init()
		f.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if f.State != huh.StateAborted {
			t.Errorf("state = %v, want aborted", f.State)
		}
	})
}
