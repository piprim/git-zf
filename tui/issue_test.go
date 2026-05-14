package tui

import (
	"slices"
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestApplyFilters(t *testing.T) {
	allRows := func() []store.IssueRow {
		return []store.IssueRow{
			{IssueSlug: "A", Title: "A"},
			{IssueSlug: "B", Title: "B", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
			{IssueSlug: "C", Title: "C", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
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
		rows := []store.IssueRow{
			{IssueSlug: "ABC-1", Title: "Fix login", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
			{IssueSlug: "ABC-2", Title: "Update signup", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
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
		rows := []store.IssueRow{
			{IssueSlug: "X-1", Title: "Fix auth", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
			{IssueSlug: "X-2", Title: "Fix auth", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
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

		rows := []store.IssueRow{
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

		rows := []store.IssueRow{
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

func TestIssueBranchPicker(t *testing.T) {
	rows := []store.BranchRow{
		{UUID: "a", IssueSlug: "A-1", Title: "First", BranchName: "feature-a"},
		{UUID: "b", IssueSlug: "B-1", Title: "Second", BranchName: "feature-b"},
	}

	t.Run("pre-selects the row matching the current branch name", func(t *testing.T) {
		var selected store.BranchRow
		IssueBranchPicker(rows, "feature-b", &selected)
		if selected.BranchName != "feature-b" {
			t.Errorf("pre-selected = %q, want %q", selected.BranchName, "feature-b")
		}
	})

	t.Run("defaults to first row when current branch is not in the list", func(t *testing.T) {
		var selected store.BranchRow
		IssueBranchPicker(rows, "not-in-list", &selected)
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

	rows := []store.IssueRow{
		{Project: "z/y"},
		{Project: "a/b"},
		{Project: ""},
		{Project: "a/b"},
		{Project: "z/y"},
	}

	got := uniqueProjects(rows)
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
