package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// A screen renders within the height of its tea.WindowSizeMsg: in a split
// terminal that is its band (see band), which cuts what exceeds it.
const (
	// minTableHeight is the least a table keeps when something opens under
	// it: its header and two rows.
	minTableHeight = 3
	// dialogChrome is what a dialog adds to its viewport: the border, a
	// blank line and the hint.
	dialogChrome = 4
)

// underTable is how many lines there are for what a table has under it,
// once its least height and the blank line between them are taken.
func underTable(height int) int { return max(1, height-minTableHeight-1) }

// fitForm sizes f, shown under a table, for a screen of height lines: huh
// fits a form to the height it is told when the form needs more, but adds
// its help line on top, so what still exceeds is taken off in a second
// pass. A screen that was told no size leaves the form alone.
func fitForm(f *huh.Form, width, height int) *huh.Form {
	if height <= 0 {
		return f
	}
	want := underTable(height)
	sized := sizeForm(f, width, want)
	if over := lipgloss.Height(sized.View()) - want; over > 0 {
		sized = sizeForm(sized, width, max(1, want-over))
	}

	return sized
}

func sizeForm(f *huh.Form, width, height int) *huh.Form {
	if m, _ := f.Update(tea.WindowSizeMsg{Width: width, Height: height}); m != nil {
		if sized, ok := m.(*huh.Form); ok {
			return sized
		}
	}

	return f
}
