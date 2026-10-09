package issue

import (
	"slices"
	"strings"
	"time"

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
	// TrackerID is the tracker's number for a repo-born issue that was
	// exported; "" otherwise (a tracker-born issue has it as IssueSlug).
	TrackerID string `json:"tracker_id"`
	// CreatedAt is when the issue was created, from its record or the
	// tracker; zero when neither says (the row then shows its branch's date).
	CreatedAt time.Time `json:"created_at,omitzero"`
}

// MatchesStatus reports whether r belongs under status ("open", "closed" or
// "all"; anything else counts as "open"). A row backed by a repo issue (State
// set) follows the issue's own state; any other row falls back to its branch
// status.
func (r *Row) MatchesStatus(status string) bool {
	switch status {
	case "all":
		return true
	case StateClosed:
		if r.State != "" {
			return r.State == StateClosed
		}

		return r.Branch != nil && r.Branch.Status == branch.StatusMerged
	default:
		if r.State != "" {
			return r.State == StateOpen
		}

		return r.Branch == nil || r.Branch.Status == branch.StatusInProgress
	}
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

// CreatedCell is the creation date of r: the issue's, or its branch's for a
// row whose issue has none, or "∅".
func CreatedCell(r *Row) string {
	created := r.CreatedAt
	if created.IsZero() && r.Branch != nil {
		created = r.Branch.CreatedAt
	}
	if created.IsZero() {
		return "∅"
	}

	return created.Format("2006-01-02")
}

// RowCells returns r's table cells in display order: issue ID, project (only
// when includeProject is set), title, branch, branch status, tracker status,
// creation date.
func RowCells(r *Row, includeProject bool) []string {
	id := r.IssueSlug
	if r.TrackerID != "" {
		id += " (#" + r.TrackerID + ")"
	}
	cells := []string{id}
	if includeProject {
		cells = append(cells, r.Project)
	}

	return append(cells,
		TitleWithLabels(r),
		BranchFieldOrEmpty(r.Branch, func(b *branch.Row) string { return b.BranchName }),
		BranchFieldOrEmpty(r.Branch, func(b *branch.Row) string { return b.Status }),
		TrackerStatusOrNA(r.TrackerStatus),
		CreatedCell(r),
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
