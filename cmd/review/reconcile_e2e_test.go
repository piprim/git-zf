package review

import (
	"context"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
)

// captureReviewPrompter records the branch list it was offered so tests can
// assert which branches a review picker would have shown. It returns nil
// (no selection) so the flow stops at the picker without running the real
// sync/request action.
type captureReviewPrompter struct {
	scriptedReviewPrompter
	seen []branch.Row
}

var _ ReviewPrompter = (*captureReviewPrompter)(nil)

func (c *captureReviewPrompter) PickBranch(ctx context.Context, title string, branches []branch.Row, current string) (*branch.Row, error) {
	c.seen = branches

	return c.scriptedReviewPrompter.PickBranch(ctx, title, branches, current)
}

func (c *captureReviewPrompter) Confirm(_ context.Context, _ string) (bool, error) { return true, nil }

// seedLocalBranch creates the local branch op.Branch off main and tracks it in
// the given status.
func seedLocalBranch(t *testing.T, rig *reviewE2ERig, op branch.Op, status string) {
	t.Helper()

	if err := rig.client.RunGitAt(t.Context(), rig.dir, "branch", op.Branch, "main"); err != nil {
		t.Fatalf("create branch %s: %v", op.Branch, err)
	}
	branchtest.Seed(t, rig.client, op, status)
}

// seedMergedElsewhere tracks a branch that still exists locally and that the
// chain records as merged: what a clone sees once it has fetched the close
// another clone made.
func seedMergedElsewhere(t *testing.T, rig *reviewE2ERig, op branch.Op) {
	t.Helper()

	seedLocalBranch(t, rig, op, branch.StatusMerged)
}

func branchSlugsOffered(seen []branch.Row) map[string]bool {
	m := make(map[string]bool, len(seen))
	for _, b := range seen {
		m[b.IssueSlug] = true
	}

	return m
}

func mergedBranchNames(t *testing.T, rig *reviewE2ERig) map[string]bool {
	t.Helper()

	merged, err := branch.ListRows(t.Context(), rig.client, branch.StatusMerged)
	if err != nil {
		t.Fatalf("ListRows merged: %v", err)
	}
	m := make(map[string]bool, len(merged))
	for _, b := range merged {
		m[b.BranchName] = true
	}

	return m
}

func TestReviewSync_ExcludesSubtaskMergedInSiblingClone(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	ctx := t.Context()

	// Parent X with two sub-tasks; X.2 was closed in a sibling clone.
	seedLocalBranch(t, rig, branch.Op{Branch: "X@feat@big", Title: "big"}, branch.StatusInProgress)
	seedLocalBranch(t, rig, branch.Op{Branch: "X.1@feat@one", Title: "one", Parent: "X"}, branch.StatusInProgress)
	seedMergedElsewhere(t, rig, branch.Op{Branch: "X.2@feat@two", Parent: "X"})

	prompter := &captureReviewPrompter{}
	if err := runReviewSyncInteractive(ctx, rig.deps(), prompter); err != nil {
		t.Fatalf("runReviewSyncInteractive: %v", err)
	}

	offered := branchSlugsOffered(prompter.seen)

	t.Run("sync picker is not offered the sibling-merged sub-task", func(t *testing.T) {
		if offered["X.2"] {
			t.Errorf("picker offered X.2 (merged in a sibling clone); offered: %+v", prompter.seen)
		}
	})

	t.Run("the still-open sub-task is still offered", func(t *testing.T) {
		if !offered["X.1"] {
			t.Errorf("picker should offer the open X.1; offered: %+v", prompter.seen)
		}
	})

	t.Run("the sibling-merged sub-task stays recorded as merged", func(t *testing.T) {
		if !mergedBranchNames(t, rig)["X.2@feat@two"] {
			t.Errorf("expected X.2@feat@two to read merged")
		}
	})
}

func TestReviewRequest_ExcludesBranchMergedInSiblingClone(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	ctx := t.Context()

	// 88 was closed in a sibling clone; the rig's default 77 is still open.
	seedMergedElsewhere(t, rig, branch.Op{Branch: "88@feat@other"})

	prompter := &captureReviewPrompter{}
	if err := runReviewRequestInteractive(ctx, rig.deps(), prompter); err != nil {
		t.Fatalf("runReviewRequestInteractive: %v", err)
	}

	offered := branchSlugsOffered(prompter.seen)

	t.Run("request picker is not offered the sibling-merged branch", func(t *testing.T) {
		if offered["88"] {
			t.Errorf("picker offered 88 (merged in a sibling clone); offered: %+v", prompter.seen)
		}
	})

	t.Run("the still-open branch is still offered", func(t *testing.T) {
		if !offered["77"] {
			t.Errorf("picker should offer the open 77; offered: %+v", prompter.seen)
		}
	})

	t.Run("the sibling-merged branch stays recorded as merged", func(t *testing.T) {
		if !mergedBranchNames(t, rig)["88@feat@other"] {
			t.Errorf("expected 88@feat@other to read merged")
		}
	})
}
