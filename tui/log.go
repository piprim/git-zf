package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	logDefaultWidth  = 80
	logDefaultHeight = 20
	// horizontalStep is the columns one ←/→ press scrolls a dialog.
	horizontalStep = 8
)

// commitHashRe matches the abbreviated hash that follows the graph characters
// of a `git log --graph --oneline` line; the line must be stripped of colors.
var commitHashRe = regexp.MustCompile(`^[^0-9a-zA-Z]*([0-9a-f]{7,40})\b`)

// commitHash returns the hash of a graph line, or "" for a graph-only line.
func commitHash(line string) string {
	m := commitHashRe.FindStringSubmatch(ansi.Strip(line))
	if m == nil {
		return ""
	}

	return m[1]
}

var cursorLineStyle = lipgloss.NewStyle().Reverse(true)

// LogModel is the `git zf log` view: the colored graph lines, a cursor, and
// enter opening the commit under it in a dialog that replaces the list. show
// renders one commit; its error is shown in place of the commit.
func LogModel(lines []string, show func(hash string) (string, error)) tea.Model {
	m := &logModel{lines: lines, visible: lines, show: show}
	m.filter = textinput.New()
	m.filter.Placeholder = "type to filter…"
	m.filter.CharLimit = 64
	m.list = viewport.New(logDefaultWidth, logDefaultHeight-2)
	m.list.SetHorizontalStep(horizontalStep)
	m.dialog = viewport.New(logDefaultWidth-4, logDefaultHeight-4)
	m.dialog.SetHorizontalStep(horizontalStep)
	m.render()

	return m
}

type logModel struct {
	lines []string
	// visible are the lines matching the filter; the cursor indexes them.
	visible   []string
	cursor    int
	list      viewport.Model
	filter    textinput.Model
	filtering bool
	show      func(hash string) (string, error)
	// viewing is set while the dialog replaces the list.
	viewing bool
	dialog  viewport.Model
}

func (*logModel) Init() tea.Cmd { return nil }

// render rebuilds the list with the cursor line highlighted and keeps the
// cursor in view.
// ponytail: O(lines) per key press; render only the visible window if a huge
// history makes it lag.
func (m *logModel) render() {
	out := make([]string, len(m.visible))
	for i, l := range m.visible {
		if i == m.cursor {
			l = cursorLineStyle.Render(ansi.Strip(l))
		}
		out[i] = l
	}
	m.list.SetContent(strings.Join(out, "\n"))

	if m.cursor < m.list.YOffset {
		m.list.SetYOffset(m.cursor)
	} else if m.cursor >= m.list.YOffset+m.list.Height {
		m.list.SetYOffset(m.cursor - m.list.Height + 1)
	}
}

func (m *logModel) moveCursor(to int) {
	m.cursor = max(0, min(to, len(m.visible)-1))
	m.render()
}

// refilter keeps the lines containing the filter text, ignoring case and
// colors, and puts the cursor on the first one.
func (m *logModel) refilter() {
	q := strings.ToLower(m.filter.Value())
	m.visible = m.visible[:0:0]
	for _, l := range m.lines {
		if q == "" || strings.Contains(strings.ToLower(ansi.Strip(l)), q) {
			m.visible = append(m.visible, l)
		}
	}
	m.moveCursor(0)
}

func (m *logModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.list.Width, m.list.Height = size.Width, size.Height-2
		m.dialog.Width, m.dialog.Height = size.Width-4, size.Height-4 // the border, its padding and the hint
		m.render()
	}
	if m.viewing {
		return m.updateViewing(msg)
	}
	if m.filtering {
		return m.updateFiltering(msg)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)

		return m, cmd
	}

	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "enter":
		m.openDialog()
	case "up", "k":
		m.moveCursor(m.cursor - 1)
	case "down", "j":
		m.moveCursor(m.cursor + 1)
	case "pgup":
		m.moveCursor(m.cursor - m.list.Height)
	case "pgdown":
		m.moveCursor(m.cursor + m.list.Height)
	case "g", "home":
		m.moveCursor(0)
	case "G", "end":
		m.moveCursor(len(m.visible) - 1)
	case "/":
		m.filtering = true

		return m, m.filter.Focus()
	default: // ←→ h l scroll horizontally
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)

		return m, cmd
	}

	return m, nil
}

func (m *logModel) updateFiltering(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.Reset()
			m.refilter()

			return m, nil
		case "enter":
			m.filtering = false
			m.filter.Blur()

			return m, nil
		}
	}

	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.refilter()

	return m, cmd
}

// openDialog shows the commit under the cursor; a graph-only line has none.
func (m *logModel) openDialog() {
	if m.cursor >= len(m.visible) {
		return
	}
	hash := commitHash(m.visible[m.cursor])
	if hash == "" {
		return
	}
	text, err := m.show(hash)
	if err != nil {
		text = "Error: " + err.Error()
	}
	m.dialog.SetContent(text)
	m.dialog.GotoTop()
	m.dialog.SetXOffset(0)
	m.viewing = true
}

func (m *logModel) updateViewing(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "q":
			m.viewing = false

			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.dialog, cmd = m.dialog.Update(msg)

	return m, cmd
}

func (m *logModel) View() string {
	if m.viewing {
		return dialogStyle.Render(m.dialog.View()) + "\n\n↑↓←→ scroll · esc: back"
	}

	if m.filtering {
		return m.list.View() + "\n\n/" + m.filter.View() + "  (esc: clear  enter: confirm)"
	}

	return m.list.View() + "\n\n↑↓ move · ←→ scroll · enter: show · / to filter · q to quit"
}
