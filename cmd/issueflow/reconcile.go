package issueflow

import (
	"context"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// ReconcileMergedFromRefs fetches refs/zf/branches/* (best-effort) and marks any
// in-progress branch whose ref has Merged=true as merged in the local store.
//
// The branch ref is the cross-machine source of truth: the clone that closes a
// branch stamps Merged=true on its ref and pushes it (see the close flow's
// updateClosedStatus). Other clones' local stores lag until reconciled, so any
// picker that lists in-progress branches straight from the store (close, review
// sync, review request) would otherwise offer a branch that was already closed
// elsewhere. Call this before listing in-progress branches for such a picker.
//
// All errors are non-fatal: the fetch is best-effort (offline ⇒ local refs
// only) and a failed status update just leaves the branch in the list, which is
// the pre-reconcile behaviour.
func ReconcileMergedFromRefs(ctx context.Context, s *store.Store, client *git.Client) {
	_ = client.FetchBranchRefs(ctx)

	branches, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return
	}

	now := time.Now()
	for _, b := range branches {
		MarkMergedFromRef(ctx, s, client, b, now)
	}
}

// MarkMergedFromRef reads branch b's refs/zf/branches/<slug> ref and, when it
// carries Merged=true, marks the branch and its issue merged in the local
// store. now is shared across a reconcile pass so a batch of reconciled
// branches share one timestamp. Errors are non-fatal (best-effort cache sync).
func MarkMergedFromRef(ctx context.Context, s *store.Store, client *git.Client, b store.BranchRow, now time.Time) {
	ref, _ := client.ReadBranchRef(ctx, b.IssueSlug)
	if ref == nil || !ref.Merged {
		return
	}

	_ = s.UpdateBranchStatus(ctx, b.BranchName, store.StatusIDMerged, &now)
	_ = s.UpdateIssueStatus(ctx, b.IssueID, store.StatusIDMerged)
}
