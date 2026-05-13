package tui

import (
	"slices"
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestApplyFilters_Open(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "C", Title: "C", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
	}

	got := applyFilters(rows, "open", "", projectAll, false)

	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}

	if got[0][0] != "A" || got[1][0] != "B" {
		t.Errorf("unexpected slugs: %q %q", got[0][0], got[1][0])
	}
}

func TestApplyFilters_Closed(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "C", Title: "C", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
	}

	got := applyFilters(rows, "closed", "", projectAll, false)

	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}

	if got[0][0] != "C" {
		t.Errorf("want C, got %q", got[0][0])
	}
}

func TestApplyFilters_All(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "C", Title: "C", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
	}

	got := applyFilters(rows, "all", "", projectAll, false)

	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
}

func TestApplyFilters_TextFilter(t *testing.T) {
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
}

func TestApplyFilters_StatusAndText(t *testing.T) {
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
}

func TestNextStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"open", "closed"},
		{"closed", "all"},
		{"all", "open"},
		{"", "open"},
	}

	for _, tc := range cases {
		if got := nextStatus(tc.in); got != tc.want {
			t.Errorf("nextStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIssueBranchPicker_preselectsCurrentBranch(t *testing.T) {
	rows := []store.BranchRow{
		{UUID: "a", IssueSlug: "A-1", Title: "First", BranchName: "feature-a"},
		{UUID: "b", IssueSlug: "B-1", Title: "Second", BranchName: "feature-b"},
	}

	var selected store.BranchRow
	IssueBranchPicker(rows, "feature-b", &selected)

	if selected.BranchName != "feature-b" {
		t.Errorf("pre-selected = %q, want %q", selected.BranchName, "feature-b")
	}
}

func TestIssueBranchPicker_defaultsToFirstWhenCurrentUnknown(t *testing.T) {
	rows := []store.BranchRow{
		{UUID: "a", IssueSlug: "A-1", Title: "First", BranchName: "feature-a"},
		{UUID: "b", IssueSlug: "B-1", Title: "Second", BranchName: "feature-b"},
	}

	var selected store.BranchRow
	IssueBranchPicker(rows, "not-in-list", &selected)

	if selected.BranchName != "feature-a" {
		t.Errorf("pre-selected = %q, want first row %q", selected.BranchName, "feature-a")
	}
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

func TestApplyFilters_ByProject(t *testing.T) {
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
}

func TestApplyFilters_AllProjects(t *testing.T) {
	t.Parallel()

	rows := []store.IssueRow{
		{IssueSlug: "A", Project: "octo/cat"},
		{IssueSlug: "B", Project: "octo/dog"},
	}

	got := applyFilters(rows, statusOpen, "", projectAll, false)
	if len(got) != 2 {
		t.Fatalf(`got %d rows with projectAll, want 2`, len(got))
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

	got := projectPickerOptions([]string{"a/b", "c/d"})
	want := []string{projectAll, "a/b", "c/d"}

	if !slices.Equal(got, want) {
		t.Errorf("projectPickerOptions = %v, want %v", got, want)
	}
}

func TestProjectPickerOptions_empty(t *testing.T) {
	t.Parallel()

	got := projectPickerOptions(nil)
	if len(got) != 1 || got[0] != projectAll {
		t.Errorf("projectPickerOptions(nil) = %v, want [%q]", got, projectAll)
	}
}
