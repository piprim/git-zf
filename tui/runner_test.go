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

func TestFormRunner_WantHistory_falseByDefault(t *testing.T) {
	r := newTestRunner()

	if r.WantHistory() {
		t.Error("WantHistory() = true, want false before any key press")
	}
}

func TestFormRunner_ctrlR_setsWantHistory(t *testing.T) {
	r := newTestRunner()

	msg := tea.KeyMsg{Type: tea.KeyCtrlR}
	model, cmd := r.Update(msg)

	if !r.WantHistory() {
		t.Error("WantHistory() = false, want true after ctrl+r")
	}

	if model != r {
		t.Error("Update must return the same *FormRunner pointer")
	}

	if cmd == nil {
		t.Error("Update must return a non-nil cmd (tea.Quit) on ctrl+r")
	}
}

func TestFormRunner_ctrlR_doesNotCrossContaminate(t *testing.T) {
	r1 := newTestRunner()
	r2 := newTestRunner()

	r1.Update(tea.KeyMsg{Type: tea.KeyCtrlR})

	if r2.WantHistory() {
		t.Error("r2.WantHistory() = true — mutation leaked between runners")
	}
}

func TestFormRunner_otherKey_doesNotSetWantHistory(t *testing.T) {
	r := newTestRunner()

	r.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if r.WantHistory() {
		t.Error("WantHistory() = true after esc, want false")
	}
}
