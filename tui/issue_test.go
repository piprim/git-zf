package tui

import (
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestApplyFilters_Open(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "C", Title: "C", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
	}

	got := applyFilters(rows, "open", "")

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

	got := applyFilters(rows, "closed", "")

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

	got := applyFilters(rows, "all", "")

	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
}

func TestApplyFilters_TextFilter(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "ABC-1", Title: "Fix login", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "ABC-2", Title: "Update signup", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
	}

	got := applyFilters(rows, "open", "login")

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

	got := applyFilters(rows, "open", "auth")

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
