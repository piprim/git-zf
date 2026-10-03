package store

import "strings"

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
