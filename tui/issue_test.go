package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/issue"
)

func TestApplyFilters(t *testing.T) {
	allRows := func() []issue.Row {
		return []issue.Row{
			{IssueSlug: "A", Title: "A"},
			{IssueSlug: "B", Title: "B", Branch: &branch.Row{Status: branch.StatusInProgress}},
			{IssueSlug: "C", Title: "C", Branch: &branch.Row{Status: branch.StatusMerged}},
		}
	}

	t.Run("open status includes no-branch and in-progress rows", func(t *testing.T) {
		got := applyFilters(allRows(), "open", "", projectAll, false, false)
		if len(got) != 2 {
			t.Fatalf("want 2 rows, got %d", len(got))
		}
		if got[0][0] != "A" || got[1][0] != "B" {
			t.Errorf("unexpected slugs: %q %q", got[0][0], got[1][0])
		}
	})

	t.Run("closed status returns only merged rows", func(t *testing.T) {
		got := applyFilters(allRows(), "closed", "", projectAll, false, false)
		if len(got) != 1 {
			t.Fatalf("want 1 row, got %d", len(got))
		}
		if got[0][0] != "C" {
			t.Errorf("want C, got %q", got[0][0])
		}
	})

	t.Run("all status returns every row regardless of branch state", func(t *testing.T) {
		got := applyFilters(allRows(), "all", "", projectAll, false, false)
		if len(got) != 3 {
			t.Fatalf("want 3 rows, got %d", len(got))
		}
	})

	t.Run("text filter matches only rows whose title contains the query", func(t *testing.T) {
		rows := []issue.Row{
			{IssueSlug: "ABC-1", Title: "Fix login", Branch: &branch.Row{Status: branch.StatusInProgress}},
			{IssueSlug: "ABC-2", Title: "Update signup", Branch: &branch.Row{Status: branch.StatusInProgress}},
		}
		got := applyFilters(rows, "open", "login", projectAll, false, false)
		if len(got) != 1 {
			t.Fatalf("want 1 row, got %d", len(got))
		}
		if got[0][0] != "ABC-1" {
			t.Errorf("want ABC-1, got %q", got[0][0])
		}
	})

	t.Run("status and text filters combine with AND semantics", func(t *testing.T) {
		rows := []issue.Row{
			{IssueSlug: "X-1", Title: "Fix auth", Branch: &branch.Row{Status: branch.StatusInProgress}},
			{IssueSlug: "X-2", Title: "Fix auth", Branch: &branch.Row{Status: branch.StatusMerged}},
		}
		got := applyFilters(rows, "open", "auth", projectAll, false, false)
		if len(got) != 1 {
			t.Fatalf("want 1 row, got %d", len(got))
		}
		if got[0][0] != "X-1" {
			t.Errorf("want X-1, got %q", got[0][0])
		}
	})

	t.Run("project filter returns only rows belonging to that project", func(t *testing.T) {
		t.Parallel()

		rows := []issue.Row{
			{IssueSlug: "A", Title: "a", Project: "octo/cat"},
			{IssueSlug: "B", Title: "b", Project: "octo/dog"},
			{IssueSlug: "C", Title: "c", Project: "octo/cat"},
		}
		got := applyFilters(rows, statusOpen, "", "octo/cat", false, false)
		if len(got) != 2 {
			t.Fatalf("got %d rows, want 2", len(got))
		}
		if got[0][0] != "A" || got[1][0] != "C" {
			t.Errorf("unexpected rows: %v", got)
		}
	})

	t.Run("projectAll sentinel passes rows from every project", func(t *testing.T) {
		t.Parallel()

		rows := []issue.Row{
			{IssueSlug: "A", Project: "octo/cat"},
			{IssueSlug: "B", Project: "octo/dog"},
		}
		got := applyFilters(rows, statusOpen, "", projectAll, false, false)
		if len(got) != 2 {
			t.Fatalf("got %d rows with projectAll, want 2", len(got))
		}
	})
}

func TestNextStatus(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"open", "closed"},
		{"closed", "all"},
		{"all", "open"},
		{"", "open"},
	}
	for _, tc := range cases {
		t.Run(tc.in+" cycles to "+tc.want, func(t *testing.T) {
			if got := nextStatus(tc.in); got != tc.want {
				t.Errorf("nextStatus(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBranchPicker(t *testing.T) {
	rows := []branch.Row{
		{IssueSlug: "A-1", Title: "First", BranchName: "feature-a"},
		{IssueSlug: "B-1", Title: "Second", BranchName: "feature-b"},
	}

	named := func(name string) func(*branch.Row) bool {
		return func(b *branch.Row) bool { return b.BranchName == name }
	}

	t.Run("pre-selects the row the predicate matches", func(t *testing.T) {
		var selected branch.Row
		BranchPicker("pick:", rows, named("feature-b"), &selected)
		if selected.BranchName != "feature-b" {
			t.Errorf("pre-selected = %q, want %q", selected.BranchName, "feature-b")
		}
	})

	t.Run("defaults to first row when no row matches", func(t *testing.T) {
		var selected branch.Row
		BranchPicker("pick:", rows, named("not-in-list"), &selected)
		if selected.BranchName != "feature-a" {
			t.Errorf("pre-selected = %q, want first row %q", selected.BranchName, "feature-a")
		}
	})
}

func TestIssueMergeStrategy_rendersGivenOptions(t *testing.T) {
	t.Parallel()

	selected := ""
	opts := []StrategyOption{
		{Value: "a", Label: "A", Hint: "first"},
		{Value: "b", Label: "B", Hint: "second"},
		{Value: "c", Label: "C", Hint: ""},
	}

	group := IssueMergeStrategy(&selected, opts)
	if group == nil {
		t.Fatal("IssueMergeStrategy returned nil group")
	}
	if selected != "a" {
		t.Errorf("selected = %q, want %q (first option's value)", selected, "a")
	}
}

func TestUniqueProjects(t *testing.T) {
	t.Parallel()

	rows := []issue.Row{
		{Project: "z/y"},
		{Project: "a/b"},
		{Project: ""},
		{Project: "a/b"},
		{Project: "z/y"},
	}

	got := issue.UniqueProjects(rows)
	want := []string{"a/b", "z/y"} // sorted, no empty, deduplicated
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestProjectPickerOptions(t *testing.T) {
	t.Parallel()

	t.Run("prepends projectAll sentinel before the given project names", func(t *testing.T) {
		t.Parallel()

		got := projectPickerOptions([]string{"a/b", "c/d"})
		want := []string{projectAll, "a/b", "c/d"}
		if !slices.Equal(got, want) {
			t.Errorf("projectPickerOptions = %v, want %v", got, want)
		}
	})

	t.Run("returns only projectAll sentinel for nil input", func(t *testing.T) {
		t.Parallel()

		got := projectPickerOptions(nil)
		if len(got) != 1 || got[0] != projectAll {
			t.Errorf("projectPickerOptions(nil) = %v, want [%q]", got, projectAll)
		}
	})
}

func TestWorktreeToggle(t *testing.T) {
	t.Parallel()

	t.Run("pre-selects false (plain branch) by default", func(t *testing.T) {
		t.Parallel()

		var useWorktree bool
		WorktreeToggle(&useWorktree)
		if useWorktree {
			t.Error("default should be false (plain branch), got true")
		}
	})
}

func TestIssueRowToTableRow_Labels(t *testing.T) {
	t.Run("labels are appended to the title cell", func(t *testing.T) {
		row := issue.RowCells(&issue.Row{IssueSlug: "1a2b3c4", Title: "Login fails", Labels: []string{"bug", "ui"}}, false)
		if row[1] != "Login fails [bug, ui]" {
			t.Errorf("title cell = %q", row[1])
		}
	})

	t.Run("no labels leaves the bare title", func(t *testing.T) {
		row := issue.RowCells(&issue.Row{IssueSlug: "1", Title: "Plain"}, false)
		if row[1] != "Plain" {
			t.Errorf("title cell = %q", row[1])
		}
	})
}

func TestFilterRows_Mine(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "A", Title: "A", AssignedToMe: true},
		{IssueSlug: "B", Title: "B"},
	}

	t.Run("mine keeps only the issues assigned to the user", func(t *testing.T) {
		got := filterRows(rows, "open", "", projectAll, false, true)
		if len(got) != 1 || got[0].IssueSlug != "A" {
			t.Errorf("rows = %+v", got)
		}
	})

	t.Run("off, every issue stays", func(t *testing.T) {
		if got := filterRows(rows, "open", "", projectAll, false, false); len(got) != 2 {
			t.Errorf("rows = %+v", got)
		}
	})
}

func TestIssueTable_MineToggle(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "A", Title: "Only A is mine", AssignedToMe: true},
		{IssueSlug: "B", Title: "B is not"},
	}
	m, err := IssueTableModel(rows, "open", IssueActions{})
	if err != nil {
		t.Fatal(err)
	}
	mKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}}

	t.Run("m keeps the issues assigned to the user and lights the tab", func(t *testing.T) {
		m, _ = m.Update(mKey)
		v := m.View()
		if strings.Contains(v, "B is not") || !strings.Contains(v, "Only A is mine") || !strings.Contains(v, "[ Mine ]") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("m again shows every issue", func(t *testing.T) {
		m, _ = m.Update(mKey)
		if v := m.View(); !strings.Contains(v, "B is not") || strings.Contains(v, "[ Mine ]") {
			t.Errorf("view = %q", v)
		}
	})
}

func TestIssueTable_EnterOpensDialog(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B"},
	}
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	newTable := func(t *testing.T, show func(string) (string, error)) tea.Model {
		t.Helper()
		m, err := IssueTableModel(rows, "open", IssueActions{Show: show})
		if err != nil {
			t.Fatal(err)
		}

		return m
	}
	var shown []string
	show := func(slug string) (string, error) {
		shown = append(shown, slug)

		return "ISSUE " + slug + " BODY", nil
	}

	t.Run("enter shows the row under the cursor in the dialog", func(t *testing.T) {
		shown = nil
		m := newTable(t, show)
		m, _ = m.Update(key(tea.KeyDown))
		m, cmd := m.Update(key(tea.KeyEnter))
		if cmd != nil {
			t.Error("enter must not quit")
		}
		if v := m.View(); !strings.Contains(v, "ISSUE B BODY") || !strings.Contains(v, "esc: back") {
			t.Errorf("view = %q", v)
		}
		if !slices.Equal(shown, []string{"B"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("esc returns to the table with the cursor kept", func(t *testing.T) {
		shown = nil
		m := newTable(t, show)
		m, _ = m.Update(key(tea.KeyDown))
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = m.Update(key(tea.KeyEsc))
		if v := m.View(); !strings.Contains(v, "enter: show") || strings.Contains(v, "ISSUE B BODY") {
			t.Errorf("view = %q", v)
		}
		m, _ = m.Update(key(tea.KeyEnter))
		if !slices.Equal(shown, []string{"B", "B"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("q closes the dialog, it does not quit", func(t *testing.T) {
		m := newTable(t, show)
		m, _ = m.Update(key(tea.KeyEnter))
		m, cmd := m.Update(runeKey('q'))
		if cmd != nil || strings.Contains(m.View(), "ISSUE A BODY") {
			t.Errorf("cmd = %v, view = %q", cmd, m.View())
		}
	})

	t.Run("enter within the filtered rows shows the matching issue", func(t *testing.T) {
		shown = nil
		m := newTable(t, show)
		m, _ = m.Update(runeKey('/'))
		m, _ = m.Update(runeKey('B'))
		m, _ = m.Update(key(tea.KeyEnter)) // leave filtering
		m, _ = m.Update(key(tea.KeyEnter))
		if !slices.Equal(shown, []string{"B"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("a show error is displayed in the dialog", func(t *testing.T) {
		m := newTable(t, func(string) (string, error) { return "", errors.New("no such issue") })
		m, _ = m.Update(key(tea.KeyEnter))
		if v := m.View(); !strings.Contains(v, "Error: no such issue") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("without a shower enter does nothing", func(t *testing.T) {
		m := newTable(t, nil)
		m, cmd := m.Update(key(tea.KeyEnter))
		if cmd != nil || !strings.Contains(m.View(), "enter: show") {
			t.Errorf("cmd = %v, view = %q", cmd, m.View())
		}
	})

	t.Run("q at the table quits", func(t *testing.T) {
		m := newTable(t, show)
		if _, cmd := m.Update(runeKey('q')); cmd == nil {
			t.Error("expected a quit command")
		}
	})
}

func TestIssueTable_EditDialog(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B"},
	}
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	type edit struct{ slug, title, description string }
	var loaded []string
	var edits []edit
	actions := IssueActions{
		Show: func(slug string) (string, error) { return "ISSUE " + slug + " BODY", nil },
		Load: func(slug string) (string, string, error) {
			loaded = append(loaded, slug)

			return "Title " + slug, "Desc " + slug, nil
		},
		Edit: func(slug, title, description string) (string, error) {
			edits = append(edits, edit{slug, title, description})

			return "Edited " + slug, nil
		},
	}
	newTable := func(t *testing.T, a IssueActions) tea.Model {
		t.Helper()
		loaded, edits = nil, nil
		m, err := IssueTableModel(rows, "open", a)
		if err != nil {
			t.Fatal(err)
		}

		return m
	}
	// complete plays the user submitting the form with title: the form
	// reaches huh.StateCompleted and the next message saves it.
	complete := func(m tea.Model, title string) tea.Model {
		model := m.(*issueTableModel)
		model.editTitle = title
		model.form.State = huh.StateCompleted
		m, _ = m.Update(key(tea.KeyTab))

		return m
	}

	t.Run("e opens the form with the issue's title and description", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(key(tea.KeyDown))
		m, cmd := m.Update(runeKey('e'))
		if cmd == nil {
			t.Error("expected the form's init command")
		}
		v := m.View()
		for _, want := range []string{"Title:", "Title B", "Desc B", "esc: cancel"} {
			if !strings.Contains(v, want) {
				t.Errorf("view misses %q:\n%s", want, v)
			}
		}
		if !slices.Equal(loaded, []string{"B"}) {
			t.Errorf("loaded = %v", loaded)
		}
	})

	t.Run("typed keys reach the form", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(runeKey('e'))
		m, _ = m.Update(runeKey('X'))
		if m.(*issueTableModel).editTitle != "Title AX" {
			t.Errorf("title = %q", m.(*issueTableModel).editTitle)
		}
	})

	t.Run("esc cancels without saving", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(runeKey('e'))
		m, _ = m.Update(key(tea.KeyEsc))
		if v := m.View(); !strings.Contains(v, "enter: show") || strings.Contains(v, "Title:") {
			t.Errorf("view = %q", v)
		}
		if len(edits) != 0 {
			t.Errorf("edits = %v", edits)
		}
	})

	t.Run("a submitted form saves, refreshes the row and shows the line", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(key(tea.KeyDown))
		m, _ = m.Update(runeKey('e'))
		m = complete(m, " New B ")
		if !slices.Equal(edits, []edit{{"B", " New B ", "Desc B"}}) {
			t.Errorf("edits = %v", edits)
		}
		v := m.View()
		for _, want := range []string{"New B", "Edited B", "enter: show"} {
			if !strings.Contains(v, want) {
				t.Errorf("view misses %q:\n%s", want, v)
			}
		}
		m, _ = m.Update(key(tea.KeyUp))
		if strings.Contains(m.View(), "Edited B") {
			t.Error("the notice must go with the next key")
		}
	})

	t.Run("e in the show dialog edits the shown issue", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(key(tea.KeyDown))
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = m.Update(runeKey('e'))
		if v := m.View(); !strings.Contains(v, "Title B") || strings.Contains(v, "ISSUE B BODY") {
			t.Errorf("view = %q", v)
		}
		m = complete(m, "New B")
		if !slices.Equal(edits, []edit{{"B", "New B", "Desc B"}}) || !strings.Contains(m.View(), "enter: show") {
			t.Errorf("edits = %v, view = %q", edits, m.View())
		}
	})

	t.Run("a load error is shown under the table", func(t *testing.T) {
		a := actions
		a.Load = func(string) (string, string, error) { return "", "", errors.New("no such issue") }
		m := newTable(t, a)
		m, _ = m.Update(runeKey('e'))
		if v := m.View(); !strings.Contains(v, "Error: no such issue") || !strings.Contains(v, "enter: show") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("a save error is shown under the table", func(t *testing.T) {
		a := actions
		a.Edit = func(string, string, string) (string, error) { return "", errors.New("title is required") }
		m := newTable(t, a)
		m, _ = m.Update(runeKey('e'))
		m = complete(m, "")
		if v := m.View(); !strings.Contains(v, "Error: title is required") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("without an editor e does nothing", func(t *testing.T) {
		m := newTable(t, IssueActions{})
		m, _ = m.Update(runeKey('e'))
		if v := m.View(); strings.Contains(v, "Title:") {
			t.Errorf("view = %q", v)
		}
	})
}

func TestIssueTable_Pick(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B"},
	}
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	newTable := func(t *testing.T, a IssueActions) tea.Model {
		t.Helper()
		m, err := IssueTableModel(rows, "open", a)
		if err != nil {
			t.Fatal(err)
		}

		return m
	}
	quits := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)

		return ok
	}
	show := func(slug string) (string, error) { return "ISSUE " + slug + " BODY", nil }

	t.Run("s in list mode quits with the row under the cursor", func(t *testing.T) {
		m := newTable(t, IssueActions{Show: show})
		m, _ = m.Update(key(tea.KeyDown))
		m, cmd := m.Update(runeKey('s'))
		pick, slug := PickedIssue(m)
		if !quits(cmd) || pick != IssuePickRow || slug != "B" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("enter in list mode still shows the issue", func(t *testing.T) {
		m := newTable(t, IssueActions{Show: show})
		m, cmd := m.Update(key(tea.KeyEnter))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "ISSUE A BODY") {
			t.Errorf("quits = %v, pick = %v, view = %q", quits(cmd), pick, m.View())
		}
	})

	t.Run("enter in pick mode quits with the row under the cursor", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true})
		m, cmd := m.Update(key(tea.KeyEnter))
		pick, slug := PickedIssue(m)
		if !quits(cmd) || pick != IssuePickRow || slug != "A" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("n in pick mode with NewIssue quits asking for a new issue", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true, NewIssue: true})
		m, cmd := m.Update(runeKey('n'))
		pick, slug := PickedIssue(m)
		if !quits(cmd) || pick != IssuePickNew || slug != "" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("n in pick mode without NewIssue does nothing", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true})
		m, cmd := m.Update(runeKey('n'))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone {
			t.Errorf("quits = %v, pick = %v", quits(cmd), pick)
		}
	})

	t.Run("q quits with nothing picked", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true})
		m, cmd := m.Update(runeKey('q'))
		if pick, slug := PickedIssue(m); !quits(cmd) || pick != IssuePickNone || slug != "" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("s on a table filtered to nothing does nothing", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true})
		m, _ = m.Update(runeKey('/'))
		for _, r := range "zzz" {
			m, _ = m.Update(runeKey(r))
		}
		m, _ = m.Update(key(tea.KeyEnter)) // confirm the filter
		m, cmd := m.Update(runeKey('s'))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone {
			t.Errorf("quits = %v, pick = %v", quits(cmd), pick)
		}
	})

	t.Run("the hint names the keys of the mode", func(t *testing.T) {
		list := newTable(t, IssueActions{Show: show}).View()
		pick := newTable(t, IssueActions{Pick: true, NewIssue: true}).View()
		record := newTable(t, IssueActions{Pick: true}).View()
		if !strings.Contains(list, "enter: show · e: edit · s: start") {
			t.Errorf("list hint missing in %q", list)
		}
		if !strings.Contains(pick, "enter: pick · n: new issue") {
			t.Errorf("pick hint missing in %q", pick)
		}
		if !strings.Contains(record, "enter: pick ·") || strings.Contains(record, "n: new issue") {
			t.Errorf("record hint wrong in %q", record)
		}
	})

	t.Run("PickedIssue on a foreign model is nothing picked", func(t *testing.T) {
		if pick, slug := PickedIssue(nil); pick != IssuePickNone || slug != "" {
			t.Errorf("pick = %v, slug = %q", pick, slug)
		}
	})
}

func TestIssueTable_PickRefusesAClosedRow(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "A", Title: "Closed one", State: issue.StateClosed},
		{IssueSlug: "B", Title: "Merged one", Branch: &branch.Row{IssueSlug: "B", Status: branch.StatusMerged}},
	}
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	quits := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)

		return ok
	}

	for name, mode := range map[string]IssueActions{"list mode": {Start: true}, "start pick mode": {Pick: true, NewIssue: true, Start: true}} {
		t.Run(name+": s on a closed record shows a notice and stays", func(t *testing.T) {
			m, err := IssueTableModel(rows, "all", mode)
			if err != nil {
				t.Fatal(err)
			}
			m, cmd := m.Update(runeKey('s'))
			pick, _ := PickedIssue(m)
			if quits(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "A is closed") {
				t.Errorf("quits = %v, pick = %v, view = %q", quits(cmd), pick, m.View())
			}
		})
	}

	t.Run("the record picker (Pick without Start) picks a closed row", func(t *testing.T) {
		m, err := IssueTableModel(rows, "all", IssueActions{Pick: true})
		if err != nil {
			t.Fatal(err)
		}
		m, cmd := m.Update(runeKey('s'))
		pick, slug := PickedIssue(m)
		if !quits(cmd) || pick != IssuePickRow || slug != "A" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("s on a merged branch row shows a notice and stays", func(t *testing.T) {
		m, err := IssueTableModel(rows, "all", IssueActions{Start: true})
		if err != nil {
			t.Fatal(err)
		}
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m, cmd := m.Update(runeKey('s'))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "B is closed") {
			t.Errorf("quits = %v, pick = %v, view = %q", quits(cmd), pick, m.View())
		}
	})
}
