package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
		got := applyFilters(allRows(), "open", "", projectAll, false)
		if len(got) != 2 {
			t.Fatalf("want 2 rows, got %d", len(got))
		}
		if got[0][0] != "A" || got[1][0] != "B" {
			t.Errorf("unexpected slugs: %q %q", got[0][0], got[1][0])
		}
	})

	t.Run("closed status returns only merged rows", func(t *testing.T) {
		got := applyFilters(allRows(), "closed", "", projectAll, false)
		if len(got) != 1 {
			t.Fatalf("want 1 row, got %d", len(got))
		}
		if got[0][0] != "C" {
			t.Errorf("want C, got %q", got[0][0])
		}
	})

	t.Run("all status returns every row regardless of branch state", func(t *testing.T) {
		got := applyFilters(allRows(), "all", "", projectAll, false)
		if len(got) != 3 {
			t.Fatalf("want 3 rows, got %d", len(got))
		}
	})

	t.Run("text filter matches only rows whose title contains the query", func(t *testing.T) {
		rows := []issue.Row{
			{IssueSlug: "ABC-1", Title: "Fix login", Branch: &branch.Row{Status: branch.StatusInProgress}},
			{IssueSlug: "ABC-2", Title: "Update signup", Branch: &branch.Row{Status: branch.StatusInProgress}},
		}
		got := applyFilters(rows, "open", "login", projectAll, false)
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
		got := applyFilters(rows, "open", "auth", projectAll, false)
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
		got := applyFilters(rows, statusOpen, "", "octo/cat", false)
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
		got := applyFilters(rows, statusOpen, "", projectAll, false)
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

func TestIssueTable_EnterOpensDialog(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B"},
	}
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	newTable := func(t *testing.T, show IssueShower) tea.Model {
		t.Helper()
		m, err := IssueTableModel(rows, "open", show)
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
