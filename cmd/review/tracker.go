package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/cmd/issueflow"
)

// maybeUpdateTrackerStatus offers to update the originating tracker's issue
// status after a review-lifecycle transition, mirroring issue close. It is a
// no-op unless (a) a tracker is configured on this clone and (b) the issue's
// BranchRef records a tracker origin.
//
// The origin signal lives in the git object (BranchRef.TrackerType), not the
// local store, so this works on a reviewer's fresh clone whose store has no row
// for the issue. For tracker-born issues the issueSlug already is the tracker
// issue ID, so it is passed straight through. All failures are non-fatal.
func maybeUpdateTrackerStatus(ctx context.Context, deps reviewDeps, prompter ReviewPrompter, issueSlug string) {
	if deps.tracker == nil {
		return // no tracker configured on this clone
	}

	// Best-effort fetch so the origin signal is current on a fresh clone.
	_ = deps.client.FetchBranchRefs(ctx)

	ref, err := deps.client.ReadBranchRef(ctx, issueSlug)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: read branch ref: %v\n", err)
		return
	}
	if ref == nil || ref.TrackerType == "" {
		return // manual issue, or a ref written before origin tracking existed
	}

	issueflow.ApplyTrackerStatus(ctx, deps.tracker, deps.client.IO().Err,
		issueSlug, deps.cfg.IssueTracker.Type, prompter.PickTrackerStatus)
}
