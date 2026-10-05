// Package branchtest seeds branch chains in the tests of other packages.
package branchtest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
)

// Seed tracks the branch op.Branch on the chain of its issue, as `issue start`
// would, then brings it to status (branch.StatusInProgress, StatusMerged or
// StatusClosed). op carries what a start op records: Title, Parent,
// TrackerType, IssueID; BranchType defaults to the type in the branch name.
// The issue slug is the one in the branch name. Nothing is pushed. The git
// client caches its remote name: add the remote before seeding when a test
// needs tracking behavior.
func Seed(t testing.TB, c *git.Client, op branch.Op, status string) {
	t.Helper()

	b, err := branch.Parse(op.Branch)
	if err != nil {
		t.Fatalf("seed branch %q: %v", op.Branch, err)
	}
	if op.BranchType == "" {
		op.BranchType = b.Type()
	}

	if err := branch.Start(t.Context(), c, b.IssueID(), &op); err != nil {
		t.Fatalf("seed branch %q: %v", op.Branch, err)
	}

	if status == branch.StatusInProgress || status == branch.StatusAll {
		return
	}

	if err := branch.SetStatus(t.Context(), c, b.IssueID(), op.Branch, status); err != nil {
		t.Fatalf("seed branch %q as %s: %v", op.Branch, status, err)
	}
}

// Amend fills the issue-level fields (Title, Parent, TrackerType, IssueID) an
// earlier Seed of op.Branch left empty. It writes a second start op for that
// branch, which adds no branch and reopens nothing. A field that already has a
// value keeps it. Nothing is pushed.
func Amend(t testing.TB, c *git.Client, op branch.Op) {
	t.Helper()

	b, err := branch.Parse(op.Branch)
	if err != nil {
		t.Fatalf("amend branch %q: %v", op.Branch, err)
	}

	op.V, op.Type, op.At = branch.OpVersion, branch.OpStart, time.Now().UTC().Format(time.RFC3339)

	payload, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("amend branch %q: %v", op.Branch, err)
	}
	if _, err := c.AppendChainCommit(t.Context(), git.BranchRefs, b.IssueID(), payload, op.Type, false); err != nil {
		t.Fatalf("amend branch %q: %v", op.Branch, err)
	}
}
