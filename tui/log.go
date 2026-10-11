package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/piprim/git-zf/config"
)

const (
	logDefaultWidth  = 80
	logDefaultHeight = 20
	// logChrome is the lines around the list: the command name and the hint.
	logChrome = 4
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

// LogActions are the git operations behind the log view.
type LogActions struct {
	// Load runs a log command and returns its lines; its error is shown in
	// place of the list.
	Load func(cmd config.LogCmd) ([]string, error)
	// Show renders one commit; its error is shown in place of the commit.
	Show func(hash string) (string, error)
}

// LogModel is the `git zf log` view: the colored lines of cmds[current], a
// cursor, enter opening the commit under it in a dialog that replaces the
// list, / filtering the lines, 1-9 and c switching the command.
func LogModel(lines []string, cmds []config.LogCmd, current int, actions LogActions) tea.Model {
	m := &logModel{lines: lines, visible: lines, cmds: cmds, current: current, actions: actions}
	m.filter = textinput.New()
	m.filter.Placeholder = "type to filter…"
	m.filter.CharLimit = 64
	m.list = viewport.New(logDefaultWidth, logDefaultHeight-logChrome)
	m.list.SetHorizontalStep(horizontalStep)
	m.dialog = viewport.New(logDefaultWidth-4, logDefaultHeight-4)
	m.dialog.SetHorizontalStep(horizontalStep)
	m.render()

	return m
}

type logModel struct {
	cmds    []config.LogCmd
	current int
	actions LogActions
	lines   []string
	// loadErr replaces the list when the current command failed.
	loadErr string
	// visible are the lines matching the filter; the cursor indexes them.
	visible   []string
	cursor    int
	list      viewport.Model
	filter    textinput.Model
	filtering bool
	// picking is set while the command picker replaces the hint.
	picking    bool
	pickCursor int
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
	if m.loadErr != "" {
		m.list.SetContent("Error: " + m.loadErr)

		return
	}
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

// switchTo loads cmds[i] and shows it, keeping the filter.
func (m *logModel) switchTo(i int) {
	m.current = i
	m.loadErr = ""
	lines, err := m.actions.Load(m.cmds[i])
	if err != nil {
		m.loadErr = err.Error()
	}
	m.lines = lines
	m.refilter()
}

// cmdWithKey returns the index of the command whose digit shortcut is key,
// or -1.
func (m *logModel) cmdWithKey(key int) int {
	for i, c := range m.cmds {
		if c.Key == key {
			return i
		}
	}

	return -1
}

func (m *logModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.list.Width, m.list.Height = size.Width, size.Height-logChrome
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
	if m.picking {
		return m.updatePicking(key)
	}

	switch k := key.String(); k {
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
	case "c":
		m.picking = true
		m.pickCursor = m.current
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if i := m.cmdWithKey(int(k[0] - '0')); i >= 0 {
			m.switchTo(i)
		}
	default: // ←→ h l scroll horizontally
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)

		return m, cmd
	}

	return m, nil
}

func (m *logModel) updatePicking(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.picking = false
	case "enter":
		m.picking = false
		m.switchTo(m.pickCursor)
	case "up", "k":
		m.pickCursor = max(0, m.pickCursor-1)
	case "down", "j":
		m.pickCursor = min(len(m.cmds)-1, m.pickCursor+1)
	case "ctrl+c":
		return m, tea.Quit
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
	if m.loadErr != "" || m.cursor >= len(m.visible) {
		return
	}
	hash := commitHash(m.visible[m.cursor])
	if hash == "" {
		return
	}
	text, err := m.actions.Show(hash)
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

	head := "Log: " + activeTabStyle.Render(m.cmds[m.current].Name) + "\n\n" + m.list.View() + "\n\n"
	switch {
	case m.filtering:
		return head + "/" + m.filter.View() + "  (esc: clear  enter: confirm)"
	case m.picking:
		names := make([]string, len(m.cmds))
		for i, c := range m.cmds {
			names[i] = c.Name
		}

		return head + renderPicker("Pick command", m.pickCursor, names)
	}

	return head + "↑↓ move · ←→ scroll · enter: show · / to filter · 1-9: command · c: pick · q to quit"
}
