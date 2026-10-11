package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/piprim/git-zf/config"
)

func TestCommitHash(t *testing.T) {
	cases := []struct {
		name, line, want string
	}{
		{"plain graph line", "* abc1234 - (HEAD -> main) feat: x 2026-10-11 (2 days ago) <a by b>", "abc1234"},
		{"nested graph line", "| | * 9f8e7d6 - fix: y", "9f8e7d6"},
		{"colored graph line", "* \x1b[31mabc1234\x1b[m -\x1b[33m (HEAD)\x1b[m feat: x", "abc1234"},
		{"graph-only line", "|/", ""},
		{"colored graph-only line", "\x1b[31m|\x1b[m\x1b[32m/\x1b[m  ", ""},
		{"merge line", "|\\  ", ""},
		{"empty line", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commitHash(c.line); got != c.want {
				t.Errorf("commitHash(%q) = %q, want %q", c.line, got, c.want)
			}
		})
	}
}

var (
	logKey     = func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	logRuneKey = func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	oneCmd     = []config.LogCmd{{Name: "graph", Key: 1}}
)

// newLog is a log view with a single command, for the tests of the list.
func newLog(lines []string, show func(string) (string, error)) tea.Model {
	return LogModel(lines, oneCmd, 0, LogActions{Show: show})
}

func TestLogModel(t *testing.T) {
	lines := []string{
		"* bbbbbbb - feat: second commit",
		"|\\  ",
		"| * aaaaaaa - chore: init",
	}
	key, runeKey := logKey, logRuneKey

	var shown []string
	show := func(hash string) (string, error) {
		shown = append(shown, hash)

		return "COMMIT " + hash + " PATCH", nil
	}

	t.Run("the view lists the lines with the command name and the hint", func(t *testing.T) {
		v := newLog(lines, show).View()
		for _, l := range lines {
			if !strings.Contains(v, strings.TrimSpace(l)) {
				t.Errorf("view %q lacks %q", v, l)
			}
		}
		if !strings.Contains(v, "Log: graph") || !strings.Contains(v, "enter: show") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("enter shows the commit under the cursor in the dialog", func(t *testing.T) {
		shown = nil
		m, cmd := newLog(lines, show).Update(key(tea.KeyEnter))
		if cmd != nil {
			t.Error("enter must not quit")
		}
		if v := m.View(); !strings.Contains(v, "COMMIT bbbbbbb PATCH") || !strings.Contains(v, "esc: back") {
			t.Errorf("view = %q", v)
		}
		if !slices.Equal(shown, []string{"bbbbbbb"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("down moves the cursor and skips nothing", func(t *testing.T) {
		shown = nil
		m, _ := newLog(lines, show).Update(runeKey('j'))
		m, _ = m.Update(key(tea.KeyDown))
		m, _ = m.Update(key(tea.KeyEnter))
		if !slices.Equal(shown, []string{"aaaaaaa"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("enter on a graph-only line does nothing", func(t *testing.T) {
		shown = nil
		m, _ := newLog(lines, show).Update(key(tea.KeyDown))
		m, cmd := m.Update(key(tea.KeyEnter))
		if cmd != nil || shown != nil || !strings.Contains(m.View(), "enter: show") {
			t.Errorf("cmd = %v, shown = %v, view = %q", cmd, shown, m.View())
		}
	})

	t.Run("esc returns to the list with the cursor kept", func(t *testing.T) {
		shown = nil
		m, _ := newLog(lines, show).Update(key(tea.KeyDown))
		m, _ = m.Update(key(tea.KeyDown))
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = m.Update(key(tea.KeyEsc))
		if v := m.View(); !strings.Contains(v, "enter: show") || strings.Contains(v, "PATCH") {
			t.Errorf("view = %q", v)
		}
		m, _ = m.Update(key(tea.KeyEnter))
		if !slices.Equal(shown, []string{"aaaaaaa", "aaaaaaa"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("the cursor stops at both ends", func(t *testing.T) {
		shown = nil
		m, _ := newLog(lines, show).Update(key(tea.KeyUp))
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = m.Update(key(tea.KeyEsc))
		for range 5 {
			m, _ = m.Update(key(tea.KeyDown))
		}
		m, _ = m.Update(key(tea.KeyEnter))
		if !slices.Equal(shown, []string{"bbbbbbb", "aaaaaaa"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("G and g jump to the last and first line", func(t *testing.T) {
		shown = nil
		m, _ := newLog(lines, show).Update(runeKey('G'))
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = m.Update(key(tea.KeyEsc))
		m, _ = m.Update(runeKey('g'))
		m, _ = m.Update(key(tea.KeyEnter))
		if !slices.Equal(shown, []string{"aaaaaaa", "bbbbbbb"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("q closes the dialog, it does not quit", func(t *testing.T) {
		m, _ := newLog(lines, show).Update(key(tea.KeyEnter))
		m, cmd := m.Update(runeKey('q'))
		if cmd != nil || strings.Contains(m.View(), "PATCH") {
			t.Errorf("cmd = %v, view = %q", cmd, m.View())
		}
	})

	t.Run("q at the list quits", func(t *testing.T) {
		if _, cmd := newLog(lines, show).Update(runeKey('q')); cmd == nil {
			t.Error("expected a quit command")
		}
	})

	t.Run("a show error is displayed in the dialog", func(t *testing.T) {
		m, _ := newLog(lines, func(string) (string, error) { return "", errors.New("bad object") }).
			Update(key(tea.KeyEnter))
		if v := m.View(); !strings.Contains(v, "Error: bad object") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("/ filters the lines to those containing the typed text", func(t *testing.T) {
		shown = nil
		m, _ := newLog(lines, show).Update(runeKey('/'))
		for _, r := range "init" {
			m, _ = m.Update(runeKey(r))
		}
		if v := m.View(); strings.Contains(v, "feat: second") || !strings.Contains(v, "chore: init") ||
			!strings.Contains(v, "/") || !strings.Contains(v, "esc: clear") {
			t.Errorf("view = %q", v)
		}
		m, _ = m.Update(key(tea.KeyEnter)) // leave filtering, keep the filter
		if v := m.View(); strings.Contains(v, "feat: second") || !strings.Contains(v, "enter: show") {
			t.Errorf("view = %q", v)
		}
		m, _ = m.Update(key(tea.KeyEnter))
		if !slices.Equal(shown, []string{"aaaaaaa"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("esc clears the filter", func(t *testing.T) {
		m, _ := newLog(lines, show).Update(runeKey('/'))
		m, _ = m.Update(runeKey('x'))
		m, _ = m.Update(key(tea.KeyEsc))
		if v := m.View(); !strings.Contains(v, "feat: second") || !strings.Contains(v, "chore: init") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("the filter ignores case and colors", func(t *testing.T) {
		colored := []string{"* \x1b[31mbbbbbbb\x1b[m - feat: \x1b[32mSecond\x1b[m", "* aaaaaaa - chore: init"}
		m, _ := newLog(colored, show).Update(runeKey('/'))
		for _, r := range "SECOND" {
			m, _ = m.Update(runeKey(r))
		}
		if v := m.View(); !strings.Contains(v, "bbbbbbb") || strings.Contains(v, "aaaaaaa") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("a filter matching nothing leaves an empty list and enter does nothing", func(t *testing.T) {
		shown = nil
		m, _ := newLog(lines, show).Update(runeKey('/'))
		m, _ = m.Update(runeKey('z'))
		m, _ = m.Update(key(tea.KeyEnter))
		m, cmd := m.Update(key(tea.KeyEnter))
		if cmd != nil || shown != nil || strings.Contains(m.View(), "aaaaaaa") {
			t.Errorf("cmd = %v, shown = %v, view = %q", cmd, shown, m.View())
		}
	})

	t.Run("the list scrolls horizontally", func(t *testing.T) {
		long := []string{"* ccccccc - a subject far longer than the twenty columns of this terminal"}
		m, _ := newLog(long, show).Update(tea.WindowSizeMsg{Width: 20, Height: 10})
		if v := m.View(); !strings.Contains(v, "* ccccccc") {
			t.Fatalf("view = %q", v)
		}
		m, _ = m.Update(key(tea.KeyRight))
		if v := m.View(); strings.Contains(v, "* ccccccc") {
			t.Errorf("view after right = %q", v)
		}
	})

	t.Run("the dialog scrolls horizontally", func(t *testing.T) {
		wide := func(string) (string, error) {
			return "+a diff line far longer than the twenty columns of this terminal", nil
		}
		m, _ := newLog(lines, wide).Update(tea.WindowSizeMsg{Width: 20, Height: 10})
		m, _ = m.Update(key(tea.KeyEnter))
		if v := m.View(); !strings.Contains(v, "+a diff") {
			t.Fatalf("view = %q", v)
		}
		m, _ = m.Update(key(tea.KeyRight))
		if v := m.View(); strings.Contains(v, "+a diff") {
			t.Errorf("view after right = %q", v)
		}
	})
}

func TestLogModel_Commands(t *testing.T) {
	key, runeKey := logKey, logRuneKey
	cmds := []config.LogCmd{
		{Name: "graph", Key: 1, Description: "every branch as a graph"},
		{Name: "linear", Key: 3},
		{Name: "tags"}, // no shortcut
	}
	graphLines := []string{"* bbbbbbb - feat: second commit", "* aaaaaaa - chore: init"}
	otherLines := map[string][]string{
		"linear": {"bbbbbbb - feat: second commit", "aaaaaaa - chore: init"},
		"tags":   {"ccccccc - (tag: v1.0) release"},
	}

	var loaded []string
	actions := LogActions{
		Load: func(c config.LogCmd) ([]string, error) {
			loaded = append(loaded, c.Name)
			if l, ok := otherLines[c.Name]; ok {
				return l, nil
			}

			return nil, errors.New("not a git log command")
		},
		Show: func(hash string) (string, error) { return "COMMIT " + hash, nil },
	}
	newLog := func() tea.Model { return LogModel(graphLines, cmds, 0, actions) }

	t.Run("the header shows the description after the name", func(t *testing.T) {
		if v := newLog().View(); !strings.Contains(v, "graph — every branch as a graph") {
			t.Errorf("view %q lacks the description", v)
		}
	})

	t.Run("a command without a description shows its name alone", func(t *testing.T) {
		m, _ := newLog().Update(runeKey('3'))
		if v := m.View(); !strings.Contains(v, "Log: linear\n") {
			t.Errorf("view %q", v)
		}
	})

	t.Run("the picker shows the descriptions", func(t *testing.T) {
		m, _ := newLog().Update(runeKey('c'))
		if v := m.View(); !strings.Contains(v, "> graph — every branch as a graph") || !strings.Contains(v, "  linear\n") {
			t.Errorf("view %q", v)
		}
	})

	t.Run("the hint offers the digits and c", func(t *testing.T) {
		if v := newLog().View(); !strings.Contains(v, "1-9: command") || !strings.Contains(v, "c: pick") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("a digit switches to the command with that key", func(t *testing.T) {
		loaded = nil
		m, _ := newLog().Update(runeKey('3'))
		if v := m.View(); !strings.Contains(v, "Log: linear") || strings.Contains(v, "* bbbbbbb") ||
			!strings.Contains(v, "bbbbbbb - feat") {
			t.Errorf("view = %q", v)
		}
		if !slices.Equal(loaded, []string{"linear"}) {
			t.Errorf("loaded = %v", loaded)
		}
	})

	t.Run("a digit with no command does nothing", func(t *testing.T) {
		loaded = nil
		m, cmd := newLog().Update(runeKey('7'))
		if cmd != nil || loaded != nil || !strings.Contains(m.View(), "Log: graph") {
			t.Errorf("cmd = %v, loaded = %v, view = %q", cmd, loaded, m.View())
		}
	})

	t.Run("the current command's digit reloads it", func(t *testing.T) {
		loaded = nil
		m, _ := newLog().Update(runeKey('3'))
		m, _ = m.Update(runeKey('3'))
		if !slices.Equal(loaded, []string{"linear", "linear"}) || !strings.Contains(m.View(), "Log: linear") {
			t.Errorf("loaded = %v, view = %q", loaded, m.View())
		}
	})

	t.Run("the filter survives a switch", func(t *testing.T) {
		m, _ := newLog().Update(runeKey('/'))
		for _, r := range "init" {
			m, _ = m.Update(runeKey(r))
		}
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = m.Update(runeKey('3'))
		if v := m.View(); strings.Contains(v, "feat: second") || !strings.Contains(v, "aaaaaaa - chore: init") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("a load error is shown in place of the list", func(t *testing.T) {
		failing := LogActions{
			Load: func(config.LogCmd) ([]string, error) { return nil, errors.New("fatal: bad revision") },
			Show: actions.Show,
		}
		m, _ := LogModel(graphLines, cmds, 0, failing).Update(runeKey('3'))
		if v := m.View(); !strings.Contains(v, "Error: fatal: bad revision") || !strings.Contains(v, "Log: linear") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("c opens a picker of the command names", func(t *testing.T) {
		m, _ := newLog().Update(runeKey('c'))
		v := m.View()
		for _, name := range []string{"graph", "linear", "tags"} {
			if !strings.Contains(v, name) {
				t.Errorf("view %q lacks %q", v, name)
			}
		}
		if !strings.Contains(v, "Pick command") || !strings.Contains(v, "> graph") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("the picker selects a command without a shortcut", func(t *testing.T) {
		loaded = nil
		m, _ := newLog().Update(runeKey('c'))
		m, _ = m.Update(runeKey('j'))
		m, _ = m.Update(key(tea.KeyDown))
		m, _ = m.Update(key(tea.KeyEnter))
		if v := m.View(); !strings.Contains(v, "Log: tags") || !strings.Contains(v, "ccccccc") ||
			strings.Contains(v, "Pick command") {
			t.Errorf("view = %q", v)
		}
		if !slices.Equal(loaded, []string{"tags"}) {
			t.Errorf("loaded = %v", loaded)
		}
	})

	t.Run("esc cancels the picker", func(t *testing.T) {
		loaded = nil
		m, _ := newLog().Update(runeKey('c'))
		m, _ = m.Update(runeKey('j'))
		m, _ = m.Update(key(tea.KeyEsc))
		if v := m.View(); strings.Contains(v, "Pick command") || !strings.Contains(v, "Log: graph") || loaded != nil {
			t.Errorf("view = %q, loaded = %v", v, loaded)
		}
	})

	t.Run("the picker opens on the current command", func(t *testing.T) {
		m, _ := newLog().Update(runeKey('3'))
		m, _ = m.Update(runeKey('c'))
		if v := m.View(); !strings.Contains(v, "> linear") {
			t.Errorf("view = %q", v)
		}
	})
}
