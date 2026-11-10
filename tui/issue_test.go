package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

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
	openShow := func(m tea.Model) (tea.Model, tea.Cmd) {
		m, _ = m.Update(key(tea.KeyEnter))
		model := m.(*issueTableModel)
		model.menuPicked = string(issueEntryShow)
		model.form.State = huh.StateCompleted

		return m.Update(key(tea.KeyTab))
	}
	var shown []string
	show := func(slug string) (string, error) {
		shown = append(shown, slug)

		return "ISSUE " + slug + " BODY", nil
	}

	t.Run("show from the menu opens the row under the cursor in the dialog", func(t *testing.T) {
		shown = nil
		m := newTable(t, show)
		m, _ = m.Update(key(tea.KeyDown))
		m, cmd := openShow(m)
		if quitsCmd(cmd) {
			t.Error("show must not quit")
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
		m, _ = openShow(m)
		m, _ = m.Update(key(tea.KeyEsc))
		if v := m.View(); !strings.Contains(v, "[enter]-Actions") || strings.Contains(v, "ISSUE B BODY") {
			t.Errorf("view = %q", v)
		}
		m, _ = openShow(m)
		if !slices.Equal(shown, []string{"B", "B"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("q closes the dialog, it does not quit", func(t *testing.T) {
		m := newTable(t, show)
		m, _ = openShow(m)
		m, cmd := m.Update(runeKey('q'))
		if cmd != nil || strings.Contains(m.View(), "ISSUE A BODY") {
			t.Errorf("cmd = %v, view = %q", cmd, m.View())
		}
	})

	t.Run("show within the filtered rows shows the matching issue", func(t *testing.T) {
		shown = nil
		m := newTable(t, show)
		m, _ = m.Update(runeKey('/'))
		m, _ = m.Update(runeKey('B'))
		m, _ = m.Update(key(tea.KeyEnter)) // leave filtering
		m, _ = openShow(m)
		if !slices.Equal(shown, []string{"B"}) {
			t.Errorf("shown = %v", shown)
		}
	})

	t.Run("a show error is displayed in the dialog", func(t *testing.T) {
		m := newTable(t, func(string) (string, error) { return "", errors.New("no such issue") })
		m, _ = openShow(m)
		if v := m.View(); !strings.Contains(v, "Error: no such issue") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("q at the table quits", func(t *testing.T) {
		m := newTable(t, show)
		if _, cmd := m.Update(runeKey('q')); cmd == nil {
			t.Error("expected a quit command")
		}
	})

	t.Run("the dialog scrolls horizontally", func(t *testing.T) {
		m := newTable(t, func(string) (string, error) {
			return "Description: a line far longer than the thirty columns of this terminal", nil
		})
		m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
		m, _ = openShow(m)
		if v := m.View(); !strings.Contains(v, "Description:") {
			t.Fatalf("view = %q", v)
		}
		m, _ = m.Update(key(tea.KeyRight))
		if v := m.View(); strings.Contains(v, "Description:") {
			t.Errorf("view after right = %q", v)
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
		if v := m.View(); !strings.Contains(v, "[enter]-Actions") || strings.Contains(v, "Title:") {
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
		for _, want := range []string{"New B", "Edited B", "[enter]-Actions"} {
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
		model := m.(*issueTableModel)
		model.menuPicked = string(issueEntryShow)
		model.form.State = huh.StateCompleted
		m, _ = m.Update(key(tea.KeyTab))
		m, _ = m.Update(runeKey('e'))
		if v := m.View(); !strings.Contains(v, "Title B") || strings.Contains(v, "ISSUE B BODY") {
			t.Errorf("view = %q", v)
		}
		m = complete(m, "New B")
		if !slices.Equal(edits, []edit{{"B", "New B", "Desc B"}}) || !strings.Contains(m.View(), "[enter]-Actions") {
			t.Errorf("edits = %v, view = %q", edits, m.View())
		}
	})

	t.Run("a load error is shown under the table", func(t *testing.T) {
		a := actions
		a.Load = func(string) (string, string, error) { return "", "", errors.New("no such issue") }
		m := newTable(t, a)
		m, _ = m.Update(runeKey('e'))
		if v := m.View(); !strings.Contains(v, "Error: no such issue") || !strings.Contains(v, "[enter]-Actions") {
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

	t.Run("enter in list mode opens the menu", func(t *testing.T) {
		m := newTable(t, IssueActions{Show: show})
		m, cmd := m.Update(key(tea.KeyEnter))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "Issue A: A") {
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

	t.Run("n in pick mode does nothing", func(t *testing.T) {
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
		pick := newTable(t, IssueActions{Pick: true}).View()
		if !strings.Contains(list, "[enter]-Actions  [e]dit  [s]tart") {
			t.Errorf("list hint missing in %q", list)
		}
		if !strings.Contains(pick, "[enter]-Pick  [q]uit") || strings.Contains(pick, "[n]ew") {
			t.Errorf("pick hint wrong in %q", pick)
		}
	})

	t.Run("the filters and the keys are two lines under 90 columns", func(t *testing.T) {
		lines := strings.Split(newTable(t, IssueActions{Show: show}).View(), "\n")
		filters, keys := lines[len(lines)-2], lines[len(lines)-1]
		if !strings.Contains(filters, "Open") || !strings.Contains(filters, "[tab]-Status  [m]ine  [/]-Filter") {
			t.Errorf("filters line = %q", filters)
		}
		for _, l := range []string{filters, keys} {
			if w := ansi.StringWidth(l); w >= 90 {
				t.Errorf("%d columns: %q", w, l)
			}
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

	for name, mode := range map[string]IssueActions{"list mode": {Start: true}, "start pick mode": {Pick: true, Start: true}} {
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

// n, c, l and r in list mode quit the table with the action picked; the
// command runs it and reopens the table. The pickers ignore c, l and r.
func TestIssueTable_ActionKeys(t *testing.T) {
	rows := []issue.Row{{IssueSlug: "A", Title: "A"}, {IssueSlug: "B", Title: "B"}}
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	quits := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)

		return ok
	}
	newTable := func(t *testing.T, a IssueActions) tea.Model {
		t.Helper()
		m, err := IssueTableModel(rows, "open", a)
		if err != nil {
			t.Fatal(err)
		}

		return m
	}

	for key, want := range map[rune]struct {
		pick IssuePick
		slug string
	}{'n': {IssuePickNew, ""}, 'c': {IssuePickClose, "B"}, 'l': {IssuePickLabel, "B"}, 'r': {IssuePickSync, ""}} {
		t.Run(string(key)+" in list mode quits with its action", func(t *testing.T) {
			m := newTable(t, IssueActions{Start: true})
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
			m, cmd := m.Update(runeKey(key))
			pick, slug := PickedIssue(m)
			if !quits(cmd) || pick != want.pick || slug != want.slug {
				t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
			}
		})
	}

	t.Run("a arms with a notice, a second a abandons the row", func(t *testing.T) {
		m := newTable(t, IssueActions{Start: true})
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m, cmd := m.Update(runeKey('a'))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "a again abandons B (no undo)") {
			t.Fatalf("first a: quits = %v, pick = %v, view = %q", quits(cmd), pick, m.View())
		}
		m, cmd = m.Update(runeKey('a'))
		if pick, slug := PickedIssue(m); !quits(cmd) || pick != IssuePickAbandon || slug != "B" {
			t.Errorf("second a: quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("any other key between two a disarms the abandon", func(t *testing.T) {
		m := newTable(t, IssueActions{Start: true})
		m, _ = m.Update(runeKey('a'))
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m, cmd := m.Update(runeKey('a'))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "a again abandons B") {
			t.Errorf("quits = %v, pick = %v, view = %q", quits(cmd), pick, m.View())
		}
	})

	for _, key := range []rune{'c', 'a', 'l', 'r'} {
		t.Run(string(key)+" in pick mode does nothing", func(t *testing.T) {
			m := newTable(t, IssueActions{Pick: true, Start: true})
			m, cmd := m.Update(runeKey(key))
			if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone {
				t.Errorf("quits = %v, pick = %v", quits(cmd), pick)
			}
		})
	}

	t.Run("the list hint names the action keys", func(t *testing.T) {
		if v := newTable(t, IssueActions{Start: true}).View(); !strings.Contains(v, "[n]ew  [c]lose  [a]bandon  [l]abel  [r]-Sync") {
			t.Errorf("hint missing in %q", v)
		}
	})
}

func TestIssueTableState(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "A", Title: "Alpha", Project: "p1", AssignedToMe: true},
		{IssueSlug: "B", Title: "Beta", Project: "p2", AssignedToMe: true},
		{IssueSlug: "C", Title: "Charlie", Project: "p1", AssignedToMe: true},
		{IssueSlug: "D", Title: "Delta", Project: "p1", State: issue.StateClosed, AssignedToMe: true},
		{IssueSlug: "E", Title: "Echo", Project: "p2", State: issue.StateClosed, AssignedToMe: true},
	}
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

	t.Run("a table rebuilt from its state is as it was left", func(t *testing.T) {
		m, err := IssueTableModel(rows, "open", IssueActions{})
		if err != nil {
			t.Fatal(err)
		}
		m, _ = m.Update(key(tea.KeyTab)) // closed
		m, _ = m.Update(runeKey('m'))    // mine
		m, _ = m.Update(runeKey('/'))
		m, _ = m.Update(runeKey('e'))
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = m.Update(key(tea.KeyDown))
		st := IssueTableState(m)
		again, err := IssueTableModelWith(rows, st, IssueActions{})
		if err != nil {
			t.Fatal(err)
		}
		if again.View() != m.View() {
			t.Errorf("views differ:\n%s\n---\n%s", again.View(), m.View())
		}
		if got := IssueTableState(again); got != st {
			t.Errorf("state = %+v, want %+v", got, st)
		}
	})

	t.Run("the project is kept", func(t *testing.T) {
		m, err := IssueTableModel(rows, "open", IssueActions{})
		if err != nil {
			t.Fatal(err)
		}
		m, _ = m.Update(runeKey('p'))
		m, _ = m.Update(key(tea.KeyDown))  // p1
		m, _ = m.Update(key(tea.KeyEnter)) // confirm
		st := IssueTableState(m)
		again, err := IssueTableModelWith(rows, st, IssueActions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := IssueTableState(again).Project; got != "p1" {
			t.Errorf("Project = %q, want p1", got)
		}
	})

	t.Run("a cursor past the rows is clamped", func(t *testing.T) {
		m, err := IssueTableModelWith(rows, IssueTableOptions{Status: "open", Cursor: 99}, IssueActions{Start: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := IssueTableState(m).Cursor; got != 2 {
			t.Errorf("Cursor = %d, want 2", got)
		}
		m, _ = m.Update(runeKey('s'))
		if _, slug := PickedIssue(m); slug != "C" {
			t.Errorf("slug = %q, want C", slug)
		}
	})

	t.Run("a project that is gone falls back to all", func(t *testing.T) {
		m, err := IssueTableModelWith(rows, IssueTableOptions{Status: "open", Project: "nope"}, IssueActions{})
		if err != nil {
			t.Fatal(err)
		}
		v := m.View()
		if !strings.Contains(v, "Alpha") || !strings.Contains(v, "Beta") {
			t.Errorf("view misses rows:\n%s", v)
		}
	})

	t.Run("an empty status is open", func(t *testing.T) {
		m1, err := IssueTableModelWith(rows, IssueTableOptions{}, IssueActions{})
		if err != nil {
			t.Fatal(err)
		}
		m2, err := IssueTableModel(rows, "", IssueActions{})
		if err != nil {
			t.Fatal(err)
		}
		if m1.View() != m2.View() {
			t.Errorf("views differ:\n%s\n---\n%s", m1.View(), m2.View())
		}
	})

	t.Run("a model that is not the table gives the zero state", func(t *testing.T) {
		if got := IssueTableState(nil); got != (IssueTableOptions{}) {
			t.Errorf("state = %+v", got)
		}
	})
}

func TestIssueMenuEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  issue.Row
		want []issueMenuEntry
	}{
		{
			"repository issue, open, no branch",
			issue.Row{State: issue.StateOpen},
			[]issueMenuEntry{issueEntryShow, issueEntryStart, issueEntryEdit, issueEntryLabel, issueEntryComment, issueEntryAbandon},
		},
		{
			"repository issue, open, branch in progress",
			issue.Row{State: issue.StateOpen, Branch: &branch.Row{Status: branch.StatusInProgress}},
			[]issueMenuEntry{issueEntryShow, issueEntryStart, issueEntryClose, issueEntryEdit, issueEntryLabel, issueEntryComment},
		},
		{
			"repository issue, open, merged branch is no branch",
			issue.Row{State: issue.StateOpen, Branch: &branch.Row{Status: branch.StatusMerged}},
			[]issueMenuEntry{issueEntryShow, issueEntryStart, issueEntryEdit, issueEntryLabel, issueEntryComment, issueEntryAbandon},
		},
		{
			"repository issue, closed",
			issue.Row{State: issue.StateClosed},
			[]issueMenuEntry{issueEntryShow, issueEntryEdit, issueEntryLabel, issueEntryComment},
		},
		{
			"not a repository issue, open, no branch",
			issue.Row{},
			[]issueMenuEntry{issueEntryShow, issueEntryStart},
		},
		{
			"not a repository issue, branch in progress",
			issue.Row{Branch: &branch.Row{Status: branch.StatusInProgress}},
			[]issueMenuEntry{issueEntryShow, issueEntryStart, issueEntryClose},
		},
		{
			"not a repository issue, merged branch",
			issue.Row{Branch: &branch.Row{Status: branch.StatusMerged}},
			[]issueMenuEntry{issueEntryShow},
		},
		{
			"not a repository issue, closed branch",
			issue.Row{Branch: &branch.Row{Status: branch.StatusClosed}},
			[]issueMenuEntry{issueEntryShow},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := issueMenuEntries(&tc.row); !slices.Equal(got, tc.want) {
				t.Errorf("issueMenuEntries = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIssueTable_Menu(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "42", Title: "Backlog", State: issue.StateOpen},
		{IssueSlug: "51", Title: "Started", State: issue.StateOpen, Branch: &branch.Row{Status: branch.StatusInProgress}},
		{IssueSlug: "7", Title: "Done", State: issue.StateClosed},
	}
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	actions := IssueActions{
		Show:  func(slug string) (string, error) { return "ISSUE " + slug + " BODY", nil },
		Load:  func(slug string) (string, string, error) { return "Title " + slug, "Desc " + slug, nil },
		Edit:  func(slug, title, description string) (string, error) { return "Edited " + slug, nil },
		Start: true,
	}
	newTable := func(t *testing.T, a IssueActions) tea.Model {
		t.Helper()
		m, err := IssueTableModel(rows, "all", a)
		if err != nil {
			t.Fatal(err)
		}

		return m
	}
	pickMenu := func(m tea.Model, entry issueMenuEntry) (tea.Model, tea.Cmd) {
		model := m.(*issueTableModel)
		model.menuPicked = string(entry)
		model.form.State = huh.StateCompleted

		return m.Update(key(tea.KeyTab))
	}

	t.Run("enter opens the menu under the table", func(t *testing.T) {
		m := newTable(t, actions)
		m, cmd := m.Update(key(tea.KeyEnter))
		if quitsCmd(cmd) {
			t.Error("enter must not quit")
		}
		v := m.View()
		for _, want := range []string{"Backlog", "Started", "Issue 42: Backlog", "show"} {
			if !strings.Contains(v, want) {
				t.Errorf("view misses %q:\n%s", want, v)
			}
		}
	})

	t.Run("esc closes the menu and the table is as it was", func(t *testing.T) {
		m := newTable(t, actions)
		before := m.View()
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = m.Update(key(tea.KeyEsc))
		if m.View() != before {
			t.Errorf("view after esc:\n%s\nwant:\n%s", m.View(), before)
		}
	})

	for _, tc := range []struct {
		entry issueMenuEntry
		row   int
		slug  string
		want  IssuePick
	}{
		{issueEntryStart, 0, "42", IssuePickRow},
		{issueEntryClose, 1, "51", IssuePickClose},
		{issueEntryLabel, 0, "42", IssuePickLabel},
		{issueEntryComment, 0, "42", IssuePickComment},
	} {
		t.Run(string(tc.entry)+" quits with its pick and the row's slug", func(t *testing.T) {
			m := newTable(t, actions)
			for range tc.row {
				m, _ = m.Update(key(tea.KeyDown))
			}
			m, _ = m.Update(key(tea.KeyEnter))
			m, cmd := pickMenu(m, tc.entry)
			pick, slug := PickedIssue(m)
			if !quitsCmd(cmd) || pick != tc.want || slug != tc.slug {
				t.Errorf("quits = %v, pick = %v, slug = %q", quitsCmd(cmd), pick, slug)
			}
		})
	}

	t.Run("show opens the show dialog", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(key(tea.KeyEnter))
		m, cmd := pickMenu(m, issueEntryShow)
		if quitsCmd(cmd) || !strings.Contains(m.View(), "ISSUE 42 BODY") {
			t.Errorf("quits = %v, view = %q", quitsCmd(cmd), m.View())
		}
	})

	t.Run("edit opens the edit form", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(key(tea.KeyEnter))
		m, cmd := pickMenu(m, issueEntryEdit)
		if quitsCmd(cmd) || !strings.Contains(m.View(), "Title 42") {
			t.Errorf("quits = %v, view = %q", quitsCmd(cmd), m.View())
		}
	})

	t.Run("abandon asks, no leaves the table", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(key(tea.KeyEnter))
		m, cmd := pickMenu(m, issueEntryAbandon)
		if quitsCmd(cmd) || !strings.Contains(m.View(), "Abandon 42? There is no undo.") {
			t.Fatalf("quits = %v, view = %q", quitsCmd(cmd), m.View())
		}
		model := m.(*issueTableModel)
		model.abandonOK = false
		model.form.State = huh.StateCompleted
		m, cmd = m.Update(key(tea.KeyTab))
		if pick, _ := PickedIssue(m); quitsCmd(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "[enter]-Actions") {
			t.Errorf("quits = %v, pick = %v, view = %q", quitsCmd(cmd), pick, m.View())
		}
	})

	t.Run("abandon, yes quits with IssuePickAbandon", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(key(tea.KeyEnter))
		m, _ = pickMenu(m, issueEntryAbandon)
		model := m.(*issueTableModel)
		model.abandonOK = true
		model.form.State = huh.StateCompleted
		m, cmd := m.Update(key(tea.KeyTab))
		if pick, slug := PickedIssue(m); !quitsCmd(cmd) || pick != IssuePickAbandon || slug != "42" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quitsCmd(cmd), pick, slug)
		}
	})

	t.Run("enter in pick mode picks the row and opens no menu", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true})
		m, cmd := m.Update(key(tea.KeyEnter))
		if pick, slug := PickedIssue(m); !quitsCmd(cmd) || pick != IssuePickRow || slug != "42" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quitsCmd(cmd), pick, slug)
		}
	})

	t.Run("the hint names the menu", func(t *testing.T) {
		v := newTable(t, actions).View()
		if !strings.Contains(v, "[enter]-Actions") || strings.Contains(v, "[enter]-Show") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("without a shower the menu has no show", func(t *testing.T) {
		a := actions
		a.Show = nil
		m := newTable(t, a)
		m, _ = m.Update(key(tea.KeyEnter))
		if v := m.View(); strings.Contains(v, "show") || !strings.Contains(v, "start") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("without Load and Edit the menu has no edit", func(t *testing.T) {
		a := actions
		a.Load = nil
		m := newTable(t, a)
		m, _ = m.Update(key(tea.KeyEnter))
		if v := m.View(); strings.Contains(v, "edit") || !strings.Contains(v, "show") {
			t.Errorf("view = %q", v)
		}
	})

	t.Run("enter on an empty table opens nothing", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(runeKey('/'))
		for _, r := range "zzz" {
			m, _ = m.Update(runeKey(r))
		}
		m, _ = m.Update(key(tea.KeyEnter))
		m, cmd := m.Update(key(tea.KeyEnter))
		if quitsCmd(cmd) || m.(*issueTableModel).form != nil {
			t.Errorf("quits = %v, form = %v", quitsCmd(cmd), m.(*issueTableModel).form)
		}
	})

	t.Run("a row with no entry opens nothing", func(t *testing.T) {
		m, err := IssueTableModel([]issue.Row{{IssueSlug: "1", Branch: &branch.Row{Status: branch.StatusMerged}}}, "all", IssueActions{})
		if err != nil {
			t.Fatal(err)
		}
		m, cmd := m.Update(key(tea.KeyEnter))
		if quitsCmd(cmd) || m.(*issueTableModel).form != nil {
			t.Errorf("quits = %v, form = %v", quitsCmd(cmd), m.(*issueTableModel).form)
		}
	})

	t.Run("q and a go to the menu, not the table", func(t *testing.T) {
		m := newTable(t, actions)
		m, _ = m.Update(key(tea.KeyEnter))
		m, cmd := m.Update(runeKey('q'))
		if quitsCmd(cmd) || m.(*issueTableModel).form == nil {
			t.Errorf("q in menu: quits = %v, form = %v", quitsCmd(cmd), m.(*issueTableModel).form)
		}
		m, cmd = m.Update(runeKey('a'))
		if quitsCmd(cmd) || strings.Contains(m.View(), "a again abandons") {
			t.Errorf("a in menu: quits = %v, view = %q", quitsCmd(cmd), m.View())
		}
	})
}
