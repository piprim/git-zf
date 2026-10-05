package issue

import (
	"slices"
	"strings"

	"github.com/piprim/git-zf/branch"
)

// Row is the unified display row for git zf issue list.
// It composes an issue identity with an optional tracked branch.
type Row struct {
	IssueSlug     string      `json:"issue_slug"`
	Title         string      `json:"title"`
	Project       string      `json:"project"`        // tracker project / repo; empty when unknown
	TrackerStatus *string     `json:"tracker_status"` // nil → display "N.A."
	Branch        *branch.Row `json:"branch"`         // nil → no branch started
	// Labels and State are set for issues stored in the repository
	// (refs/zf/issues/*). State is "open" or "closed"; "" means the row has no
	// repo issue and its status is derived from the branch.
	Labels []string `json:"labels"`
	State  string   `json:"state"`
}

// BranchFieldOrEmpty returns fn(b) or "∅" when b is nil.
func BranchFieldOrEmpty(b *branch.Row, fn func(*branch.Row) string) string {
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
func TitleWithLabels(r *Row) string {
	if len(r.Labels) == 0 {
		return r.Title
	}

	return r.Title + " [" + strings.Join(r.Labels, ", ") + "]"
}

// RowCells returns r's table cells in display order: issue ID, project (only
// when includeProject is set), title, branch, branch status, tracker status,
// creation date.
func RowCells(r *Row, includeProject bool) []string {
	cells := []string{r.IssueSlug}
	if includeProject {
		cells = append(cells, r.Project)
	}

	return append(cells,
		TitleWithLabels(r),
		BranchFieldOrEmpty(r.Branch, func(b *branch.Row) string { return b.BranchName }),
		BranchFieldOrEmpty(r.Branch, func(b *branch.Row) string { return b.Status }),
		TrackerStatusOrNA(r.TrackerStatus),
		BranchFieldOrEmpty(r.Branch, func(b *branch.Row) string { return b.CreatedAt.Format("2006-01-02") }),
	)
}

// UniqueProjects returns the deduplicated, sorted list of non-empty
// Row.Project values. A table shows its project column only when there is more
// than one.
func UniqueProjects(rows []Row) []string {
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
