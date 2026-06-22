package issue

import (
	"github.com/piprim/git-zf/tracker"
)

// IssueStartFlags configures how the issue-start flow acquires and names a
// branch. Variant and ParentIssueSlug feed branch naming, so this stays in the
// domain package next to the entity rather than moving to cmd/issueflow.
type IssueStartFlags struct {
	TrackerFirst    bool
	Variant         string
	ParentIssueSlug string // non-empty when this is a sub-task; value is the parent's IssueSlug
}

// Issue is the in-flow domain entity for a work item: a tracker.Issue enriched
// with the conventional-commit Type that drives branch naming. It is one of
// three distinct "Issue" shapes in the codebase, each owning a different layer:
//
//   - tracker.Issue — the tracker-agnostic wire shape (all strings) returned by
//     a tracker backend. Embedded below as the external/source representation.
//   - issue.Issue (this type) — the domain entity used while a branch is being
//     started: a tracker.Issue plus the branch Type (feat/fix/doc…).
//   - store.Issue — the SQLite persistence row (int64 PK, StatusID, and a
//     *string TrackerType where nil means a manual entry). The durable record
//     after the branch exists.
//
// The data flows tracker.Issue (fetched) → issue.Issue (typed in the form) →
// store.Issue (persisted).
type Issue struct {
	Type string // feat, fix, doc, etc…
	tracker.Issue
}
