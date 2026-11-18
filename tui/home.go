package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/piprim/git-zf/git"
)

// HomeData is what the home screen shows above its entries. A part that is
// empty is left out.
type HomeData struct {
	Prog string
	// Branch is the current branch, "detached at <hash>", or "".
	Branch string
	// Issue is the ID and title of the branch's issue, or "".
	Issue string
	// Upstream is the upstream's short name ("origin/main"), "" without one.
	Upstream string
	// HasAB is false when the upstream is gone: no counts are shown.
	HasAB         bool
	Ahead, Behind int
	// Review is the review status of the branch's issue in plain words, or "".
	Review string
	// Entries is the working tree, for the counts and the status view.
	Entries []git.StatusEntry
}

// HomeEntry is one command of the home screen: its key, the command's name,
// its label and a hint that says what waits there ("" for none).
type HomeEntry struct{ Key, Name, Label, Hint string }

// HomePickKind is how the home screen was left.
type HomePickKind int

const (
	HomeQuit HomePickKind = iota
	HomeRun
	HomeRefresh
)

// HomePick is what the user chose; Name is the entry's Name for HomeRun.
type HomePick struct {
	Kind HomePickKind
	Name string
}

var (
	homeProgStyle   = lipgloss.NewStyle().Bold(true)
	homeBranchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	homeCursorStyle = lipgloss.NewStyle().Bold(true)
	homeKeyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

type homeModel struct {
	data    HomeData
	entries []HomeEntry
	cursor  int
	pick    HomePick
	width   int
	height  int
	// viewing is set while the status view replaces the menu.
	viewing bool
	status  viewport.Model
}

// NewHomeModel builds the home screen. cursor is the Name of the entry to
// start on; "" starts on commit when the tree has something to commit, on
// issue otherwise.
func NewHomeModel(data HomeData, entries []HomeEntry, cursor string) tea.Model {
	if cursor == "" {
		cursor = "issue"
		if c := CountStatus(data.Entries); c.Staged+c.Modified+c.Unmerged > 0 {
			cursor = "commit"
		}
	}
	m := &homeModel{data: data, entries: entries, status: viewport.New(logDefaultWidth, logDefaultHeight)}
	for i, e := range entries {
		if e.Name == cursor {
			m.cursor = i
		}
	}

	return m
}

// PickedHome returns how a home model was left; a quit for any other model.
func PickedHome(m tea.Model) HomePick {
	if h, ok := m.(*homeModel); ok {
		return h.pick
	}

	return HomePick{}
}

func (m *homeModel) Init() tea.Cmd { return nil }

func (m *homeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.status.Width, m.status.Height = msg.Width, max(msg.Height-2, 1) // the hint and its blank line
		if m.viewing {
			m.status.SetContent(cutLines(m.statusText(), m.status.Width))
		}
	case tea.KeyMsg:
		if m.viewing {
			return m.updateViewing(msg)
		}

		return m.updateMenu(msg)
	}

	return m, nil
}

func (m *homeModel) updateViewing(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "q":
		m.viewing = false

		return m, nil
	case "ctrl+c":
		return m.quitWith(HomePick{})
	}
	var cmd tea.Cmd
	m.status, cmd = m.status.Update(key)

	return m, cmd
}

func (m *homeModel) updateMenu(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The screen's own keys come first: an entry can never take them.
	switch k := key.String(); k {
	case "q", "ctrl+c":
		return m.quitWith(HomePick{})
	case "r":
		return m.quitWith(HomePick{Kind: HomeRefresh})
	case "s":
		m.viewing = true
		// Cut here, with a mark: the viewport cuts a wide line silently.
		m.status.SetContent(cutLines(m.statusText(), m.status.Width))
		m.status.GotoTop()
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, len(m.entries)-1)
	case "enter":
		if m.cursor >= 0 && m.cursor < len(m.entries) {
			return m.quitWith(HomePick{Kind: HomeRun, Name: m.entries[m.cursor].Name})
		}
	default:
		for i, e := range m.entries {
			if e.Key == k {
				m.cursor = i

				return m.quitWith(HomePick{Kind: HomeRun, Name: e.Name})
			}
		}
	}

	return m, nil
}

func (m *homeModel) quitWith(pick HomePick) (tea.Model, tea.Cmd) {
	m.pick = pick

	return m, tea.Quit
}

func (m *homeModel) View() string {
	// The program runs inline: the last frame would stay above the command's
	// output and the next home screen. q keeps it, as a table does.
	if m.pick.Kind != HomeQuit {
		return ""
	}
	if m.viewing {
		return m.fit(m.status.View() + "\n\n" + keyHints("↑/↓", "scroll", "esc", "back"))
	}

	head := homeProgStyle.Render(m.data.Prog)
	if m.data.Branch != "" {
		head += "  " + homeBranchStyle.Render(m.data.Branch)
	}
	if m.data.Issue != "" {
		head += " · " + m.data.Issue
	}
	lines := []string{head, strings.Repeat(" ", lipgloss.Width(m.data.Prog)+2) + m.summary(), ""}

	labelWidth := 0
	for _, e := range m.entries {
		labelWidth = max(labelWidth, lipgloss.Width(e.Label))
	}
	for i, e := range m.entries {
		line := "  " + homeKeyStyle.Render(e.Key) + "  " + e.Label
		if i == m.cursor {
			line = "> " + homeKeyStyle.Render(e.Key) + "  " + homeCursorStyle.Render(e.Label)
		}
		if e.Hint != "" {
			line += strings.Repeat(" ", labelWidth-lipgloss.Width(e.Label)+4) + descStyle.Render(e.Hint)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", keyHints("↑/↓", "move", "enter", "run", "s", "status", "r", "refresh", "q", "quit"))

	// The inline renderer drops the top of a view taller than the terminal,
	// which is the header: give up the blank lines first, then the hint.
	if m.height > 0 && len(lines) > m.height {
		lines = slices.DeleteFunc(lines, func(l string) bool { return l == "" })
	}
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:len(lines)-1]
	}

	return m.fit(strings.Join(lines, "\n"))
}

// fit cuts every line to the terminal's width: a wrapped line would break the
// inline renderer's line count.
func (m *homeModel) fit(s string) string {
	if m.width <= 0 {
		return s
	}

	return cutLines(s, m.width)
}

// cutLines cuts every line of s to width cells, a cut line ending with "…".
func cutLines(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}

	return strings.Join(lines, "\n")
}

// summary is the header's second line: the working tree, the upstream and
// the review, each left out when it has nothing to say.
func (m *homeModel) summary() string {
	c := CountStatus(m.data.Entries)
	var tree []string
	for _, p := range []struct {
		n    int
		word string
	}{{c.Staged, "staged"}, {c.Modified, "modified"}, {c.Untracked, "untracked"}, {c.Unmerged, "unmerged"}} {
		if p.n > 0 {
			tree = append(tree, fmt.Sprintf("%d %s", p.n, p.word))
		}
	}
	parts := []string{"clean"}
	if len(tree) > 0 {
		parts[0] = strings.Join(tree, ", ")
	}

	if remote, _, _ := strings.Cut(m.data.Upstream, "/"); m.data.Upstream != "" {
		if m.data.HasAB {
			parts = append(parts, fmt.Sprintf("↑%d ↓%d %s", m.data.Ahead, m.data.Behind, remote))
		} else {
			parts = append(parts, remote+" (gone)")
		}
	}
	if m.data.Review != "" {
		parts = append(parts, "review: "+m.data.Review)
	}

	return strings.Join(parts, " · ")
}

// statusText is the status view: the branch line, then the files as `git
// status` groups them.
func (m *homeModel) statusText() string {
	head := m.summary()
	if m.data.Branch != "" {
		head = homeBranchStyle.Render(m.data.Branch) + "  " + head
	}
	panel := StatusPanel(m.data.Entries, false, false)
	if panel == "" {
		panel = "Nothing to commit, working tree clean"
	}

	return head + "\n\n" + panel
}
