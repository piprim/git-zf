package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
)

// maybeUpdateTrackerStatus offers to update the originating tracker's issue
// status after a review-lifecycle transition, mirroring issue close. It is a
// no-op unless (a) a tracker is configured on this clone and (b) the issue's
// branch chain records a tracker origin.
//
// The origin signal lives on the chain (refs/zf/branches/<slug>), fetched by
// every clone, so this works on a reviewer's fresh clone too. For tracker-born issues the issueSlug already is the tracker
// issue ID, so it is passed straight through. All failures are non-fatal.
func maybeUpdateTrackerStatus(ctx context.Context, deps reviewDeps, prompter ReviewPrompter, issueSlug string) {
	if trackerBornIssue(ctx, deps, issueSlug) {
		applyTrackerStatus(ctx, deps, prompter, issueSlug)
	}
}

// applyTrackerStatus offers the tracker's status list and applies the pick.
// Callers must have checked trackerBornIssue first.
func applyTrackerStatus(ctx context.Context, deps reviewDeps, prompter ReviewPrompter, issueSlug string) {
	issueflow.ApplyTrackerStatus(ctx, deps.tracker, deps.client.IO().Err,
		issueSlug, deps.cfg.IssueTracker.Type, prompter.PickTrackerStatus)
}

// addTrackerComment posts the rejection reason for the given round as a
// comment on the originating tracker issue. Best-effort: failures are warnings.
// Callers must have checked trackerBornIssue first.
func addTrackerComment(ctx context.Context, deps reviewDeps, issueSlug string, round int, reason string) {
	body := fmt.Sprintf("Changes requested (review round %d):\n\n%s", round, reason)
	if err := deps.tracker.AddComment(ctx, issueSlug, body); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: add tracker comment: %v\n", err)
	}
}

// trackerBornIssue reports whether issueSlug originated in a configured
// tracker: a tracker is wired on this clone and the branch chain records a
// tracker type. Manual issues and pre-origin refs return false.
func trackerBornIssue(ctx context.Context, deps reviewDeps, issueSlug string) bool {
	if deps.tracker == nil {
		return false // no tracker configured on this clone
	}

	// Best-effort fetch so the origin signal is current on a fresh clone.
	_ = branch.Fetch(ctx, deps.client)

	ref, err := branch.Load(ctx, deps.client, issueSlug)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: read branch ref: %v\n", err)
		return false
	}
	return ref != nil && ref.TrackerType != ""
}
