package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/piprim/git-zf/git"
)

func TestCountStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		xy   []string
		want StatusCounts
	}{
		{"staged", []string{"M "}, StatusCounts{Staged: 1}},
		{"modified", []string{" M"}, StatusCounts{Modified: 1}},
		{"staged and modified again", []string{"MM"}, StatusCounts{Staged: 1, Modified: 1}},
		{"deleted in the worktree", []string{" D"}, StatusCounts{Modified: 1}},
		{"untracked", []string{"??"}, StatusCounts{Untracked: 1}},
		{"unmerged", []string{"UU"}, StatusCounts{Unmerged: 1}},
		{"no entry", nil, StatusCounts{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var entries []git.StatusEntry
			for _, xy := range tc.xy {
				entries = append(entries, git.StatusEntry{XY: xy, Path: "f.go"})
			}
			if got := CountStatus(entries); got != tc.want {
				t.Errorf("CountStatus = %+v, want %+v", got, tc.want)
			}
		})
	}
}

var homeTestEntries = []HomeEntry{
	{Key: "c", Name: "commit", Label: "Commit"},
	{Key: "i", Name: "issue", Label: "Issue"},
	{Key: "b", Name: "branch", Label: "Branch"},
	{Key: "v", Name: "review", Label: "Review"},
	{Key: "l", Name: "log", Label: "Log"},
}

// homeKeys feeds keys to a home model and returns it with the last command.
func homeKeys(m tea.Model, keys ...tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		m, cmd = m.Update(k)
	}

	return m, cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)

	return ok
}

var (
	keyEnter = tea.KeyMsg{Type: tea.KeyEnter}
	keyEsc   = tea.KeyMsg{Type: tea.KeyEsc}
	keyDown  = tea.KeyMsg{Type: tea.KeyDown}
	keyCtrlC = tea.KeyMsg{Type: tea.KeyCtrlC}
)

func TestHomeModel_Keys(t *testing.T) {
	t.Parallel()

	home := func(cursor string) tea.Model { return NewHomeModel(HomeData{Prog: "git-zf"}, homeTestEntries, cursor) }

	t.Run("a letter runs its command", func(t *testing.T) {
		t.Parallel()

		m, cmd := homeKeys(home(""), runeKey('v'))
		if got := PickedHome(m); got != (HomePick{Kind: HomeRun, Name: "review"}) || !isQuit(cmd) {
			t.Errorf("pick = %+v quit = %v, want review and a quit", got, isQuit(cmd))
		}
	})

	t.Run("enter runs the entry under the cursor", func(t *testing.T) {
		t.Parallel()

		m, cmd := homeKeys(home("log"), keyEnter)
		if got := PickedHome(m); got != (HomePick{Kind: HomeRun, Name: "log"}) || !isQuit(cmd) {
			t.Errorf("pick = %+v, want log", got)
		}
	})

	t.Run("down then enter runs the next entry", func(t *testing.T) {
		t.Parallel()

		m, _ := homeKeys(home("commit"), keyDown, keyEnter)
		if got := PickedHome(m); got != (HomePick{Kind: HomeRun, Name: "issue"}) {
			t.Errorf("pick = %+v, want issue", got)
		}
	})

	t.Run("r asks for a refresh", func(t *testing.T) {
		t.Parallel()

		m, cmd := homeKeys(home(""), runeKey('r'))
		if got := PickedHome(m); got != (HomePick{Kind: HomeRefresh}) || !isQuit(cmd) {
			t.Errorf("pick = %+v, want a refresh", got)
		}
	})

	t.Run("q quits", func(t *testing.T) {
		t.Parallel()

		m, cmd := homeKeys(home(""), runeKey('q'))
		if got := PickedHome(m); got != (HomePick{Kind: HomeQuit}) || !isQuit(cmd) {
			t.Errorf("pick = %+v quit = %v, want a quit", got, isQuit(cmd))
		}
	})

	t.Run("ctrl+c quits", func(t *testing.T) {
		t.Parallel()

		m, cmd := homeKeys(home(""), keyCtrlC)
		if got := PickedHome(m); got != (HomePick{Kind: HomeQuit}) || !isQuit(cmd) {
			t.Errorf("pick = %+v quit = %v, want a quit", got, isQuit(cmd))
		}
	})

	t.Run("esc on the menu does nothing", func(t *testing.T) {
		t.Parallel()

		m, cmd := homeKeys(home(""), keyEsc)
		if cmd != nil {
			t.Fatal("esc returned a command")
		}
		m, _ = homeKeys(m, runeKey('c'))
		if got := PickedHome(m); got != (HomePick{Kind: HomeRun, Name: "commit"}) {
			t.Errorf("pick after esc = %+v, want commit", got)
		}
	})

	t.Run("s opens the status view and esc returns", func(t *testing.T) {
		t.Parallel()

		m, _ := homeKeys(home(""), runeKey('s'))
		if !strings.Contains(ansi.Strip(m.View()), "Nothing to commit, working tree clean") {
			t.Fatalf("status view = %q", ansi.Strip(m.View()))
		}
		m, cmd := homeKeys(m, runeKey('c'))
		if cmd != nil {
			t.Error("c in the status view returned a command")
		}
		m, _ = homeKeys(m, keyEsc, runeKey('c'))
		if got := PickedHome(m); got != (HomePick{Kind: HomeRun, Name: "commit"}) {
			t.Errorf("pick after esc = %+v, want commit", got)
		}
	})

	t.Run("q in the status view returns to the menu", func(t *testing.T) {
		t.Parallel()

		m, cmd := homeKeys(home(""), runeKey('s'), runeKey('q'))
		if cmd != nil {
			t.Error("q in the status view returned a command")
		}
		if !strings.Contains(ansi.Strip(m.View()), "Commit") {
			t.Errorf("view after q = %q, want the entries", ansi.Strip(m.View()))
		}
	})

	for _, key := range []rune{'v', 'r'} {
		t.Run("the screen is wiped when "+string(key)+" leaves it", func(t *testing.T) {
			t.Parallel()

			// The program runs inline: a last frame left behind would stack
			// under the command's output and the next home screen.
			if m, _ := homeKeys(home(""), runeKey(key)); m.View() != "" {
				t.Errorf("view after %c = %q, want nothing", key, m.View())
			}
		})
	}

	t.Run("a model that is not a home model picks a quit", func(t *testing.T) {
		t.Parallel()

		if got := PickedHome(nil); got != (HomePick{Kind: HomeQuit}) {
			t.Errorf("pick = %+v", got)
		}
	})
}

func TestHomeModel_Cursor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		xy     string
		cursor string
		want   string
	}{
		{"starts on commit with staged files", "M ", "", "commit"},
		{"starts on commit with modified files", " M", "", "commit"},
		{"starts on commit with unmerged files", "UU", "", "commit"},
		{"starts on issue on a clean tree", "", "", "issue"},
		{"starts on issue with untracked files only", "??", "", "issue"},
		{"starts on the given entry", "M ", "branch", "branch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var d HomeData
			if tc.xy != "" {
				d.Entries = []git.StatusEntry{{XY: tc.xy, Path: "f.go"}}
			}
			m, _ := homeKeys(NewHomeModel(d, homeTestEntries, tc.cursor), keyEnter)
			if got := PickedHome(m).Name; got != tc.want {
				t.Errorf("first cursor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHomeModel_View(t *testing.T) {
	t.Parallel()

	lines := func(m tea.Model) []string { return strings.Split(ansi.Strip(m.View()), "\n") }
	full := HomeData{
		Prog: "git-zf", Branch: "42@feat@login-form", Issue: "42 Login form rejects unicode",
		Upstream: "origin/42@feat@login-form", HasAB: true, Ahead: 2, Review: "changes requested",
		Entries: []git.StatusEntry{
			{XY: "M ", Path: "a"}, {XY: "M ", Path: "b"}, {XY: "MM", Path: "c"}, {XY: " M", Path: "d"}, {XY: "??", Path: "e"},
		},
	}

	t.Run("full header", func(t *testing.T) {
		t.Parallel()

		l := lines(NewHomeModel(full, homeTestEntries, ""))
		if !strings.Contains(l[0], "git-zf  42@feat@login-form · 42 Login form rejects unicode") {
			t.Errorf("line 1 = %q", l[0])
		}
		if !strings.Contains(l[1], "3 staged, 2 modified, 1 untracked · ↑2 ↓0 origin · review: changes requested") {
			t.Errorf("line 2 = %q", l[1])
		}
	})

	t.Run("clean tree without upstream or review", func(t *testing.T) {
		t.Parallel()

		l := lines(NewHomeModel(HomeData{Prog: "git-zf", Branch: "main"}, homeTestEntries, ""))
		if strings.TrimSpace(l[1]) != "clean" {
			t.Errorf("line 2 = %q, want clean alone", l[1])
		}
	})

	t.Run("upstream gone", func(t *testing.T) {
		t.Parallel()

		l := lines(NewHomeModel(HomeData{Prog: "git-zf", Branch: "x", Upstream: "origin/x"}, homeTestEntries, ""))
		if !strings.Contains(l[1], "origin (gone)") || strings.Contains(l[1], "↑") {
			t.Errorf("line 2 = %q", l[1])
		}
	})

	t.Run("no issue", func(t *testing.T) {
		t.Parallel()

		l := lines(NewHomeModel(HomeData{Prog: "git-zf", Branch: "main"}, homeTestEntries, ""))
		if strings.Contains(l[0], " · ") {
			t.Errorf("line 1 = %q, want no separator", l[0])
		}
	})

	t.Run("hints", func(t *testing.T) {
		t.Parallel()

		entries := []HomeEntry{
			{Key: "c", Name: "commit", Label: "Commit", Hint: "3 staged files"},
			{Key: "l", Name: "log", Label: "Log"},
		}
		view := ansi.Strip(NewHomeModel(HomeData{}, entries, "").View())
		if !strings.Contains(view, "3 staged files") {
			t.Errorf("view = %q, want the commit hint", view)
		}
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, "Log") && l != strings.TrimRight(l, " ") {
				t.Errorf("log line %q has trailing text", l)
			}
		}
	})

	t.Run("narrow terminal cuts the header", func(t *testing.T) {
		t.Parallel()

		m, _ := NewHomeModel(full, homeTestEntries, "").Update(tea.WindowSizeMsg{Width: 30, Height: 20})
		raw := strings.Split(m.View(), "\n")
		for _, l := range raw {
			if w := lipgloss.Width(l); w > 30 {
				t.Errorf("line %q is %d cells wide", ansi.Strip(l), w)
			}
		}
		if !strings.HasSuffix(ansi.Strip(raw[0]), "…") {
			t.Errorf("line 1 = %q, want a cut", ansi.Strip(raw[0]))
		}
	})

	t.Run("status view marks the lines it cuts", func(t *testing.T) {
		t.Parallel()

		long := strings.Repeat("deep/", 30) + "file.go"
		d := HomeData{Entries: []git.StatusEntry{{XY: "??", Path: long}}}
		check := func(t *testing.T, m tea.Model, width int) {
			t.Helper()
			cut := false
			for _, l := range strings.Split(m.View(), "\n") {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("line %q is %d cells wide, want at most %d", ansi.Strip(l), w, width)
				}
				cut = cut || (strings.Contains(l, "deep/") && strings.HasSuffix(ansi.Strip(l), "…"))
			}
			if !cut {
				t.Errorf("the long path is not marked as cut at %d:\n%s", width, ansi.Strip(m.View()))
			}
		}

		m, _ := NewHomeModel(d, homeTestEntries, "").Update(tea.WindowSizeMsg{Width: 60, Height: 20})
		m, _ = homeKeys(m, runeKey('s'))
		check(t, m, 60)
		m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 20}) // resized while viewing
		check(t, m, 40)
	})

	t.Run("a short terminal keeps the header", func(t *testing.T) {
		t.Parallel()

		for height, wantHint := range map[int]bool{8: true, 7: false} {
			m, _ := NewHomeModel(full, homeTestEntries, "").Update(tea.WindowSizeMsg{Width: 120, Height: height})
			l := lines(m)
			if len(l) > height || !strings.Contains(l[0], "git-zf") || !strings.Contains(l[1], "3 staged") {
				t.Errorf("height %d: %d lines, header = %q / %q", height, len(l), l[0], l[1])
			}
			if got := strings.Contains(l[len(l)-1], "[q]uit"); got != wantHint {
				t.Errorf("height %d: hint shown = %v, want %v", height, got, wantHint)
			}
			for _, e := range homeTestEntries {
				if !strings.Contains(strings.Join(l, "\n"), e.Label) {
					t.Errorf("height %d: entry %s is missing", height, e.Label)
				}
			}
		}
	})

	t.Run("status view lists the files", func(t *testing.T) {
		t.Parallel()

		d := HomeData{Entries: []git.StatusEntry{{XY: "M ", Path: "a.go"}, {XY: "??", Path: "b.go"}}}
		m, _ := homeKeys(NewHomeModel(d, homeTestEntries, ""), runeKey('s'))
		view := ansi.Strip(m.View())
		for _, want := range []string{"a.go", "b.go", "Untracked", "1 staged, 1 untracked"} {
			if !strings.Contains(view, want) {
				t.Errorf("status view lacks %q:\n%s", want, view)
			}
		}
	})
}
