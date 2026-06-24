package review

import (
	"context"
	"testing"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// captureReviewPrompter records the branch list it was offered so tests can
// assert which branches a review picker would have shown. It returns nil
// (no selection) so the flow stops at the picker without running the real
// sync/request action.
type captureReviewPrompter struct {
	scriptedReviewPrompter
	seen []store.BranchRow
}

var _ ReviewPrompter = (*captureReviewPrompter)(nil)

func (c *captureReviewPrompter) PickBranch(ctx context.Context, title string, branches []store.BranchRow, current string) (*store.BranchRow, error) {
	c.seen = branches

	return c.scriptedReviewPrompter.PickBranch(ctx, title, branches, current)
}

// seedMergedElsewhere inserts an in-progress issue+branch into the store and
// stamps its branch ref Merged=true, simulating a clone that closed it (the ref
// is the cross-machine source of truth; this clone's store still lags).
func seedMergedElsewhere(t *testing.T, rig *reviewE2ERig, slug, branchName string) {
	t.Helper()

	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: slug, Title: slug, StatusID: store.StatusIDInProgress},
		&store.Branch{Name: branchName, Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed %s: %v", slug, err)
	}
	if _, err := rig.client.WriteBranchRef(t.Context(), slug, git.BranchRef{
		IssueSlug:  slug,
		BranchName: branchName,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Merged:     true,
	}); err != nil {
		t.Fatalf("seed %s merged ref: %v", slug, err)
	}
}

func branchSlugsOffered(seen []store.BranchRow) map[string]bool {
	m := make(map[string]bool, len(seen))
	for _, b := range seen {
		m[b.IssueSlug] = true
	}

	return m
}

func mergedBranchNames(t *testing.T, rig *reviewE2ERig) map[string]bool {
	t.Helper()

	merged, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
	if err != nil {
		t.Fatalf("ListBranches merged: %v", err)
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
	if err := rig.store.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: "X", Title: "big", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "X@feat@big", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed X: %v", err)
	}
	if err := rig.store.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: "X.1", Title: "one", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "X.1@feat@one", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed X.1: %v", err)
	}
	seedMergedElsewhere(t, rig, "X.2", "X.2@feat@two")

	if err := rig.store.InsertIssueRelation(ctx, "X", "X.1"); err != nil {
		t.Fatalf("relation X→X.1: %v", err)
	}
	if err := rig.store.InsertIssueRelation(ctx, "X", "X.2"); err != nil {
		t.Fatalf("relation X→X.2: %v", err)
	}

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

	t.Run("store reconciles the sibling-merged sub-task to merged", func(t *testing.T) {
		if !mergedBranchNames(t, rig)["X.2@feat@two"] {
			t.Errorf("expected X.2@feat@two reconciled to merged in store")
		}
	})
}

func TestReviewRequest_ExcludesBranchMergedInSiblingClone(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	ctx := t.Context()

	// 88 was closed in a sibling clone; the rig's default 77 is still open.
	seedMergedElsewhere(t, rig, "88", "88@feat@other")

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

	t.Run("store reconciles the sibling-merged branch to merged", func(t *testing.T) {
		if !mergedBranchNames(t, rig)["88@feat@other"] {
			t.Errorf("expected 88@feat@other reconciled to merged in store")
		}
	})
}
