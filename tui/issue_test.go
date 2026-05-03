package tui

import "testing"

func TestIssueStatusFilter_defaultsToOpen(t *testing.T) {
	var status string
	IssueStatusFilter(&status, "")
	if status != "open" {
		t.Errorf("status = %q, want %q", status, "open")
	}
}

func TestIssueStatusFilter_preservesSelected(t *testing.T) {
	var status string
	IssueStatusFilter(&status, "closed")
	if status != "closed" {
		t.Errorf("status = %q, want %q", status, "closed")
	}
}
