package tui

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewLayout(t *testing.T) {
	for _, c := range []struct {
		name             string
		rows, cols, size int
		top              int
		ok               bool
	}{
		{"half of 40 rows", 40, 100, 50, 20, true},
		{"half of 24 rows", 24, 80, 50, 12, true},
		{"git-zf's band under the floor", 19, 80, 50, 9, false},
		{"the output band under the floor", 24, 80, 80, 19, false},
		{"80 percent of 50 rows", 50, 80, 80, 40, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			l, ok := newLayout(c.rows, c.cols, c.size)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if want := (layout{rows: c.rows, cols: c.cols, top: c.top}); ok && l != want {
				t.Errorf("layout = %+v, want %+v", l, want)
			}
		})
	}
}

func TestLayout_Sequences(t *testing.T) {
	l := layout{rows: 24, cols: 3, top: 12}
	for _, c := range []struct{ name, got, want string }{
		{"open scrolls the screen away and sets the region", l.open(), "\x1b[24;1H" + strings.Repeat("\n", 24) + "\x1b[13;24r\x1b[13;1H"},
		{"before saves the output cursor and goes to the band", l.before(), "\x1b7\x1b[13;24r\x1b[1;1H"},
		{"after draws the separator and comes back", l.after(), "\x1b[12;1H───\x1b[13;24r\x1b8"},
		{"resized moves the region's bottom", l.resized(30), "\x1b[13;30r\x1b[12;1H"},
		{"close slides the output to the top", l.closeSeq(), "\x1b7\x1b[r\x1b[1;1H\x1b[12M\x1b8\x1b[12A"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

// stubScreen records what a wrapped screen is sent and shows a set view.
type stubScreen struct {
	view string
	size tea.WindowSizeMsg
	last tea.Msg
}

func (s *stubScreen) Init() tea.Cmd { return nil }

func (s *stubScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	s.last = msg
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		s.size = ws
	}

	return s, nil
}

func (s *stubScreen) View() string { return s.view }

func numbered(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line " + string(rune('A'+i%26)) + strings.Repeat("x", i/26)
	}

	return strings.Join(lines, "\n")
}

func TestBand(t *testing.T) {
	newBand := func(view string) (*band, *stubScreen, *bytes.Buffer) {
		inner, out := &stubScreen{view: view}, &bytes.Buffer{}

		return &band{inner: inner, l: layout{rows: 24, cols: 80, top: 12}, out: out}, inner, out
	}
	sep := strings.Repeat("─", 80)

	t.Run("reports the band's height", func(t *testing.T) {
		b, inner, _ := newBand("")
		b.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		if inner.size.Height != 11 || inner.size.Width != 80 {
			t.Errorf("inner saw %+v, want 80x11", inner.size)
		}
	})

	t.Run("pads a short view and ends on the separator", func(t *testing.T) {
		b, _, _ := newBand("a\nb")
		lines := strings.Split(b.View(), "\n")
		if len(lines) != 12 || lines[0] != "a" || lines[1] != "b" || lines[2] != "" || lines[11] != sep {
			t.Errorf("%d lines: %q", len(lines), lines)
		}
	})

	t.Run("cuts a tall view", func(t *testing.T) {
		b, _, _ := newBand(numbered(40))
		lines := strings.Split(b.View(), "\n")
		if len(lines) != 12 || lines[10] != "line K" || lines[11] != sep {
			t.Errorf("%d lines, 11th %q, last %q", len(lines), lines[10], lines[len(lines)-1])
		}
	})

	t.Run("an empty view is a blank band", func(t *testing.T) {
		b, _, _ := newBand("")
		if lines := strings.Split(b.View(), "\n"); len(lines) != 12 || lines[11] != sep {
			t.Errorf("%d lines: %q", len(lines), lines)
		}
	})

	t.Run("a trailing newline adds no line", func(t *testing.T) {
		b, _, _ := newBand("a\n")
		if lines := strings.Split(b.View(), "\n"); len(lines) != 12 || lines[1] != "" {
			t.Errorf("%d lines: %q", len(lines), lines)
		}
	})

	t.Run("same rows writes nothing", func(t *testing.T) {
		b, _, out := newBand("")
		b.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		if out.Len() != 0 {
			t.Errorf("wrote %q", out.String())
		}
	})

	t.Run("a resize moves the region", func(t *testing.T) {
		b, _, out := newBand("")
		b.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
		if out.String() != "\x1b[13;30r\x1b[12;1H" || b.l.rows != 30 {
			t.Errorf("wrote %q, rows %d", out.String(), b.l.rows)
		}
	})

	t.Run("a horizontal resize widens the separator", func(t *testing.T) {
		b, _, out := newBand("")
		b.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		lines := strings.Split(b.View(), "\n")
		// the region is set again: a terminal may drop it on any resize
		if lines[11] != strings.Repeat("─", 100) || out.String() != "\x1b[13;24r\x1b[12;1H" {
			t.Errorf("separator %d runes, wrote %q", len([]rune(lines[11])), out.String())
		}
	})

	t.Run("shrunk to the band or less writes nothing", func(t *testing.T) {
		b, _, out := newBand("")
		b.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
		if out.Len() != 0 || b.l.rows != 12 {
			t.Errorf("wrote %q, rows %d", out.String(), b.l.rows)
		}
	})

	t.Run("passes other messages through", func(t *testing.T) {
		b, inner, _ := newBand("")
		key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
		b.Update(key)
		if got, ok := inner.last.(tea.KeyMsg); !ok || got.String() != "q" {
			t.Errorf("inner saw %#v", inner.last)
		}
	})

	t.Run("keeps the inner model the screen returns", func(t *testing.T) {
		b, inner, _ := newBand("")
		m, _ := b.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if got, ok := m.(*band); !ok || got.inner != tea.Model(inner) {
			t.Errorf("Update returned %#v", m)
		}
	})
}
