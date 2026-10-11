package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func TestLogModel(t *testing.T) {
	lines := []string{
		"* bbbbbbb - feat: second commit",
		"|\\  ",
		"| * aaaaaaa - chore: init",
	}
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

	var shown []string
	show := func(hash string) (string, error) {
		shown = append(shown, hash)

		return "COMMIT " + hash + " PATCH", nil
	}

	t.Run("the view lists the lines with the hint", func(t *testing.T) {
		v := LogModel(lines, show).View()
		for _, l := range lines {
			if !strings.Contains(v, strings.TrimSpace(l)) {
				t.Errorf("view %q lacks %q", v, l)
			}
		}
		if !strings.Contains(v, "enter: show") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("enter shows the commit under the cursor in the dialog", func(t *testing.T) {
		shown = nil
		m, cmd := LogModel(lines, show).Update(key(tea.KeyEnter))
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
		m, _ := LogModel(lines, show).Update(runeKey('j'))
		m, _ = m.Update(key(tea.KeyDown))
		m, _ = m.Update(key(tea.KeyEnter))
		if !slices.Equal(shown, []string{"aaaaaaa"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("enter on a graph-only line does nothing", func(t *testing.T) {
		shown = nil
		m, _ := LogModel(lines, show).Update(key(tea.KeyDown))
		m, cmd := m.Update(key(tea.KeyEnter))
		if cmd != nil || shown != nil || !strings.Contains(m.View(), "enter: show") {
			t.Errorf("cmd = %v, shown = %v, view = %q", cmd, shown, m.View())
		}
	})

	t.Run("esc returns to the list with the cursor kept", func(t *testing.T) {
		shown = nil
		m, _ := LogModel(lines, show).Update(key(tea.KeyDown))
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
		m, _ := LogModel(lines, show).Update(key(tea.KeyUp))
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
		m, _ := LogModel(lines, show).Update(runeKey('G'))
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = m.Update(key(tea.KeyEsc))
		m, _ = m.Update(runeKey('g'))
		m, _ = m.Update(key(tea.KeyEnter))
		if !slices.Equal(shown, []string{"aaaaaaa", "bbbbbbb"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("q closes the dialog, it does not quit", func(t *testing.T) {
		m, _ := LogModel(lines, show).Update(key(tea.KeyEnter))
		m, cmd := m.Update(runeKey('q'))
		if cmd != nil || strings.Contains(m.View(), "PATCH") {
			t.Errorf("cmd = %v, view = %q", cmd, m.View())
		}
	})

	t.Run("q at the list quits", func(t *testing.T) {
		if _, cmd := LogModel(lines, show).Update(runeKey('q')); cmd == nil {
			t.Error("expected a quit command")
		}
	})

	t.Run("a show error is displayed in the dialog", func(t *testing.T) {
		m, _ := LogModel(lines, func(string) (string, error) { return "", errors.New("bad object") }).
			Update(key(tea.KeyEnter))
		if v := m.View(); !strings.Contains(v, "Error: bad object") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("/ filters the lines to those containing the typed text", func(t *testing.T) {
		shown = nil
		m, _ := LogModel(lines, show).Update(runeKey('/'))
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
		m, _ := LogModel(lines, show).Update(runeKey('/'))
		m, _ = m.Update(runeKey('x'))
		m, _ = m.Update(key(tea.KeyEsc))
		if v := m.View(); !strings.Contains(v, "feat: second") || !strings.Contains(v, "chore: init") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("the filter ignores case and colors", func(t *testing.T) {
		colored := []string{"* \x1b[31mbbbbbbb\x1b[m - feat: \x1b[32mSecond\x1b[m", "* aaaaaaa - chore: init"}
		m, _ := LogModel(colored, show).Update(runeKey('/'))
		for _, r := range "SECOND" {
			m, _ = m.Update(runeKey(r))
		}
		if v := m.View(); !strings.Contains(v, "bbbbbbb") || strings.Contains(v, "aaaaaaa") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("a filter matching nothing leaves an empty list and enter does nothing", func(t *testing.T) {
		shown = nil
		m, _ := LogModel(lines, show).Update(runeKey('/'))
		m, _ = m.Update(runeKey('z'))
		m, _ = m.Update(key(tea.KeyEnter))
		m, cmd := m.Update(key(tea.KeyEnter))
		if cmd != nil || shown != nil || strings.Contains(m.View(), "aaaaaaa") {
			t.Errorf("cmd = %v, shown = %v, view = %q", cmd, shown, m.View())
		}
	})

	t.Run("the list scrolls horizontally", func(t *testing.T) {
		long := []string{"* ccccccc - a subject far longer than the twenty columns of this terminal"}
		m, _ := LogModel(long, show).Update(tea.WindowSizeMsg{Width: 20, Height: 10})
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
		m, _ := LogModel(lines, wide).Update(tea.WindowSizeMsg{Width: 20, Height: 10})
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
