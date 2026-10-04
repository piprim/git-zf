package store

import (
	"slices"
	"strings"
)

// BranchFieldOrEmpty returns fn(b) or "∅" when b is nil.
func BranchFieldOrEmpty(b *BranchRow, fn func(*BranchRow) string) string {
	if b == nil {
		return "∅"
	}

	return fn(b)
}

// TrackerStatusOrNA returns *s or "N.A." when s is nil.
func TrackerStatusOrNA(s *string) string {
	if s == nil {
		return "N.A."
	}

	return *s
}

// TitleWithLabels returns the row title followed by its labels in brackets,
// e.g. "Login fails [bug, ui]", or the bare title when there are none.
// ponytail: labels share the Title cell instead of getting their own column;
// add a column if the title gets truncated too often in practice.
func TitleWithLabels(r *IssueRow) string {
	if len(r.Labels) == 0 {
		return r.Title
	}

	return r.Title + " [" + strings.Join(r.Labels, ", ") + "]"
}

// IssueRowCells returns r's table cells in display order: issue ID, project
// (only when includeProject is set), title, branch, local status, tracker
// status, creation date.
func IssueRowCells(r *IssueRow, includeProject bool) []string {
	cells := []string{r.IssueSlug}
	if includeProject {
		cells = append(cells, r.Project)
	}

	return append(cells,
		TitleWithLabels(r),
		BranchFieldOrEmpty(r.Branch, func(b *BranchRow) string { return b.BranchName }),
		BranchFieldOrEmpty(r.Branch, func(b *BranchRow) string { return string(b.Status) }),
		TrackerStatusOrNA(r.TrackerStatus),
		BranchFieldOrEmpty(r.Branch, func(b *BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
	)
}

// UniqueProjects returns the deduplicated, sorted list of non-empty
// IssueRow.Project values. A table shows its project column only when there
// is more than one.
func UniqueProjects(rows []IssueRow) []string {
	seen := make(map[string]struct{})
	for _, r := range rows {
		if r.Project == "" {
			continue
		}

		seen[r.Project] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}

	slices.Sort(out)

	return out
}
