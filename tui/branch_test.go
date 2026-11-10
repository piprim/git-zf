package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/piprim/git-zf/branch"
)

func TestBranchConflictPicker_returnsGroup(t *testing.T) {
	t.Parallel()

	var action string
	g := BranchConflictPicker("ABC-42@feat@x", &action)

	if g == nil {
		t.Fatal("BranchConflictPicker returned nil")
	}
}

func TestVariantLabelInput_returnsGroup(t *testing.T) {
	t.Parallel()

	var label string
	g := VariantLabelInput(&label)

	if g == nil {
		t.Fatal("VariantLabelInput returned nil")
	}
}

func branchTestRows(now time.Time) []branch.Row {
	return []branch.Row{
		{BranchName: "master", Plain: true, Current: true, LastCommit: now.Add(-2 * time.Hour)},
		{BranchName: "42@feat@assign", IssueSlug: "42", Type: "feat", Status: branch.StatusInProgress, Title: "Assign", CreatedAt: now.AddDate(0, 0, -3), LastCommit: now.AddDate(0, 0, -1)},
		{BranchName: "51@fix@prune", IssueSlug: "51", Type: "fix", Status: branch.StatusMerged, Title: "Prune", CreatedAt: now.AddDate(0, -1, 0), LastCommit: now.AddDate(0, 0, -21)},
		{BranchName: "spike", Plain: true, LastCommit: now.AddDate(0, -5, 0)},
		{BranchName: "remote-thing", Plain: true, RemoteOnly: true, LastCommit: now.AddDate(-2, 0, 0)},
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"hours", now.Add(-2 * time.Hour), "2h ago"},
		{"days", now.AddDate(0, 0, -1), "1d ago"},
		{"weeks", now.AddDate(0, 0, -21), "3w ago"},
		{"months", now.AddDate(0, -5, 0), "5mo ago"},
		{"years", now.AddDate(-2, 0, 0), "2y ago"},
		{"zero", time.Time{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativeTime(tc.at, now); got != tc.want {
				t.Errorf("relativeTime = %q, want %q", got, tc.want)
			}
		})
	}
}

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func typeText(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(runeKey(r))
	}

	return m
}

func quitsCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)

	return ok
}

func TestBranchTable_Rows(t *testing.T) {
	rows := branchTestRows(time.Now())
	opts := BranchTableOptions{Types: []string{"feat", "fix"}, Remote: "origin"}

	t.Run("remote-only rows are hidden at start", func(t *testing.T) {
		if v := BranchTableModel(rows, opts).View(); strings.Contains(v, "remote-thing") {
			t.Errorf("view shows remote-thing:\n%s", v)
		}
	})
	t.Run("R shows them with the remote prefix", func(t *testing.T) {
		m, _ := BranchTableModel(rows, opts).Update(runeKey('R'))
		if v := m.View(); !strings.Contains(v, "origin/remote-thing") {
			t.Errorf("view lacks origin/remote-thing:\n%s", v)
		}
	})
	t.Run("the current branch is starred", func(t *testing.T) {
		if v := BranchTableModel(rows, opts).View(); !strings.Contains(v, "* master") {
			t.Errorf("view lacks \"* master\":\n%s", v)
		}
	})
	t.Run("a plain row has blank Created", func(t *testing.T) {
		if v := BranchTableModel(rows, opts).View(); strings.Contains(v, "0001-01-01") {
			t.Errorf("view shows a zero date:\n%s", v)
		}
	})
	t.Run("the footer lists the keys", func(t *testing.T) {
		v := BranchTableModel(rows, opts).View()
		for _, want := range []string{"S status", "T type", "R remotes", "P prune-tracker", "q quit"} {
			if !strings.Contains(v, want) {
				t.Errorf("footer lacks %q:\n%s", want, v)
			}
		}
	})
}

func TestBranchTable_Filters(t *testing.T) {
	rows := branchTestRows(time.Now())
	opts := BranchTableOptions{Types: []string{"feat", "fix"}, Remote: "origin"}
	shows := func(t *testing.T, v string, want []string, unwanted []string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(v, w) {
				t.Errorf("view lacks %q:\n%s", w, v)
			}
		}
		for _, u := range unwanted {
			if strings.Contains(v, u) {
				t.Errorf("view shows %q:\n%s", u, v)
			}
		}
	}

	t.Run("/ narrows by text", func(t *testing.T) {
		m, _ := BranchTableModel(rows, opts).Update(runeKey('/'))
		m = typeText(m, "spi")
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		shows(t, m.View(), []string{"spike"}, []string{"42@feat@assign", "master"})
	})
	t.Run("S opens a form rendered under the table", func(t *testing.T) {
		m, _ := BranchTableModel(rows, opts).Update(runeKey('S'))
		shows(t, m.View(), []string{"Branch", "in_progress", "plain"}, nil)
	})
	t.Run("esc on the S form keeps the previous statuses", func(t *testing.T) {
		m, _ := BranchTableModel(rows, opts).Update(runeKey('S'))
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if m.(*branchTableModel).form != nil {
			t.Error("form still open")
		}
		shows(t, m.View(), []string{"42@feat@assign", "51@fix@prune", "spike"}, nil)
	})
	t.Run("statuses applied from options hide the others", func(t *testing.T) {
		o := opts
		o.Statuses = []string{branch.StatusMerged}
		shows(t, BranchTableModel(rows, o).View(), []string{"51@fix@prune"}, []string{"42@feat@assign", "spike"})
	})
	t.Run("plain is a status", func(t *testing.T) {
		o := opts
		o.Statuses = []string{BranchStatusPlain}
		shows(t, BranchTableModel(rows, o).View(), []string{"spike", "master"}, []string{"42@feat@assign", "51@fix@prune"})
	})
	t.Run("none is a type", func(t *testing.T) {
		m := BranchTableModel(rows, opts)
		m.(*branchTableModel).setTypes([]string{BranchTypeNone})
		shows(t, m.View(), []string{"spike", "master"}, []string{"42@feat@assign", "51@fix@prune"})
	})
	t.Run("no status checked shows No branch matches and the footer", func(t *testing.T) {
		m := BranchTableModel(rows, opts)
		m.(*branchTableModel).setStatuses([]string{})
		shows(t, m.View(), []string{"No branch matches.", "S status"}, []string{"spike", "master"})
	})
	t.Run("active filters are named above the footer", func(t *testing.T) {
		o := opts
		o.Statuses = []string{branch.StatusMerged}
		shows(t, BranchTableModel(rows, o).View(), []string{"status: merged"}, nil)
		shows(t, BranchTableModel(rows, opts).View(), nil, []string{"status:"})
	})
}

func TestBranchTable_Picks(t *testing.T) {
	rows := branchTestRows(time.Now())
	opts := BranchTableOptions{Types: []string{"feat", "fix"}, Remote: "origin"}

	t.Run("enter picks the row under the cursor", func(t *testing.T) {
		m, cmd := BranchTableModel(rows, opts).Update(tea.KeyMsg{Type: tea.KeyEnter})
		pick, row := PickedBranch(m)
		if !quitsCmd(cmd) || pick != BranchPickRow || row == nil || row.BranchName != rows[0].BranchName {
			t.Errorf("quits = %v, pick = %v, row = %+v", quitsCmd(cmd), pick, row)
		}
	})
	for key, want := range map[rune]BranchPick{'n': BranchPickNew, 'p': BranchPickPrune, 'P': BranchPickPruneTracker, 'q': BranchPickNone} {
		t.Run(string(key)+" quits with its pick", func(t *testing.T) {
			m, cmd := BranchTableModel(rows, opts).Update(runeKey(key))
			pick, row := PickedBranch(m)
			if !quitsCmd(cmd) || pick != want || row != nil {
				t.Errorf("quits = %v, pick = %v, row = %+v", quitsCmd(cmd), pick, row)
			}
		})
	}
	t.Run("enter on an empty table picks nothing and does not quit", func(t *testing.T) {
		m := BranchTableModel(rows, opts)
		m.(*branchTableModel).setStatuses([]string{})
		m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		pick, row := PickedBranch(m)
		if quitsCmd(cmd) || pick != BranchPickNone || row != nil {
			t.Errorf("quits = %v, pick = %v, row = %+v", quitsCmd(cmd), pick, row)
		}
	})
}

func TestBranchTable_Review(t *testing.T) {
	rows := branchTestRows(time.Now())
	opts := BranchTableOptions{Types: []string{"feat", "fix"}, Remote: "origin"}

	t.Run("enter still picks a row after a filter emptied the table", func(t *testing.T) {
		m, _ := BranchTableModel(rows, opts).Update(runeKey('/'))
		m = typeText(m, "zzz")
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		pick, row := PickedBranch(m)
		if !quitsCmd(cmd) || pick != BranchPickRow || row == nil || row.BranchName != "master" {
			t.Errorf("quits = %v, pick = %v, row = %+v", quitsCmd(cmd), pick, row)
		}
	})
	t.Run("a type outside the config is shown and offered by T", func(t *testing.T) {
		extra := append(branchTestRows(time.Now()), branch.Row{BranchName: "7@chore@odd", IssueSlug: "7", Type: "chore", Status: branch.StatusInProgress})
		m := BranchTableModel(extra, opts)
		if v := m.View(); !strings.Contains(v, "7@chore@odd") {
			t.Errorf("view lacks the chore row:\n%s", v)
		}
		m, _ = m.Update(runeKey('T'))
		if v := m.View(); !strings.Contains(v, "chore") {
			t.Errorf("T form lacks chore:\n%s", v)
		}
	})
	t.Run("a tracked row with an empty type counts as none", func(t *testing.T) {
		extra := []branch.Row{{BranchName: "8@x@legacy", IssueSlug: "8", Status: branch.StatusInProgress}}
		if v := BranchTableModel(extra, opts).View(); !strings.Contains(v, "8@x@legacy") {
			t.Errorf("view lacks the legacy row:\n%s", v)
		}
	})
	t.Run("the state of a model restores its filters and cursor", func(t *testing.T) {
		m := BranchTableModel(rows, opts)
		m.(*branchTableModel).setStatuses([]string{branch.StatusMerged, BranchStatusPlain})
		m, _ = m.Update(runeKey('R'))
		m, _ = m.Update(runeKey('/'))
		m = typeText(m, "e")
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		again := BranchTableModel(rows, BranchTableState(m))
		if again.View() != m.View() {
			t.Errorf("views differ:\n%s\n---\n%s", again.View(), m.View())
		}
		_, row := PickedBranch(func() tea.Model { r, _ := again.Update(tea.KeyMsg{Type: tea.KeyEnter}); return r }())
		_, want := PickedBranch(func() tea.Model { r, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); return r }())
		if row == nil || want == nil || row.BranchName != want.BranchName {
			t.Errorf("cursor row = %+v, want %+v", row, want)
		}
	})
}
