package review

import (
	"context"
	"testing"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// withFakeTracker attaches a fake tracker to the rig and sets the issue-tracker
// type, mirroring the close-flow rig (cmd/issue/close_e2e_test.go). Returns the
// fake so tests can assert on RecordedUpdates.
func withFakeTracker(t *testing.T, rig *reviewE2ERig) *fake.Tracker {
	t.Helper()

	rig.cfg.IssueTracker.Type = "fake"
	rawT, err := tracker.New(rig.cfg.IssueTracker)
	if err != nil {
		t.Fatalf("tracker.New: %v", err)
	}
	fakeT, ok := rawT.(*fake.Tracker)
	if !ok {
		t.Fatalf("tracker.New returned %T, want *fake.Tracker", rawT)
	}
	rig.tracker = fakeT
	return fakeT
}

// seedBranchRef writes a BranchRef for slug carrying the given tracker type.
// An empty trackerType yields a manual ref (no prompt expected).
func seedBranchRef(t *testing.T, rig *reviewE2ERig, slug, branchName, trackerType string) {
	t.Helper()

	if _, err := rig.client.WriteBranchRef(context.Background(), slug, git.BranchRef{
		IssueSlug:   slug,
		BranchName:  branchName,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		TrackerType: trackerType,
	}); err != nil {
		t.Fatalf("WriteBranchRef: %v", err)
	}
}

// inProgressBranchRow returns the seeded in-progress BranchRow for slug.
func inProgressBranchRow(t *testing.T, rig *reviewE2ERig, slug string) *store.BranchRow {
	t.Helper()

	branches, err := rig.store.ListBranches(context.Background(), store.BranchStatusInProgress)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	for i := range branches {
		if branches[i].IssueSlug == slug {
			return &branches[i]
		}
	}
	t.Fatalf("no in-progress branch row for slug %q", slug)
	return nil
}

// bringToInReview runs review request so the issue is locked in_review, then
// clears any tracker updates recorded by the request step. This isolates the
// approve/reject assertions to the transition under test.
func bringToInReview(t *testing.T, ctx context.Context, rig *reviewE2ERig, fakeT *fake.Tracker) {
	t.Helper()

	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout feature branch: %v", err)
	}
	picked := inProgressBranchRow(t, rig, "77")
	p := &scriptedReviewPrompter{Branch: picked, TrackerStatus: "In Progress"}
	if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
		t.Fatalf("review request (setup): %v", err)
	}
	fakeT.RecordedUpdates = nil
}

func assertOneUpdate(t *testing.T, fakeT *fake.Tracker) {
	t.Helper()

	if got := len(fakeT.RecordedUpdates); got != 1 {
		t.Fatalf("RecordedUpdates len = %d, want 1", got)
	}
	got := fakeT.RecordedUpdates[0]
	want := fake.Update{IssueID: "77", StatusName: "In Progress"}
	if got != want {
		t.Errorf("RecordedUpdates[0] = %+v, want %+v", got, want)
	}
}

func TestReviewTracker_Request_TrackerBornIssue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)
	fakeT := withFakeTracker(t, rig)
	seedBranchRef(t, rig, "77", "77@feat@my-feature", "fake")

	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout feature branch: %v", err)
	}
	picked := inProgressBranchRow(t, rig, "77")
	p := &scriptedReviewPrompter{Branch: picked, TrackerStatus: "In Progress"}

	if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
		t.Fatalf("runReviewRequestInteractive: %v", err)
	}

	t.Run("records the picked tracker status", func(t *testing.T) {
		assertOneUpdate(t, fakeT)
	})
}

func TestReviewTracker_Approve_TrackerBornIssue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)
	fakeT := withFakeTracker(t, rig)
	seedBranchRef(t, rig, "77", "77@feat@my-feature", "fake")
	bringToInReview(t, ctx, rig, fakeT)

	p := &scriptedReviewPrompter{
		Branch:        &store.BranchRow{IssueSlug: "77", BranchName: "77@review"},
		TrackerStatus: "In Progress",
	}
	if err := runReviewApproveInteractive(ctx, rig.deps(), p); err != nil {
		t.Fatalf("runReviewApproveInteractive: %v", err)
	}

	t.Run("records the picked tracker status", func(t *testing.T) {
		assertOneUpdate(t, fakeT)
	})
}

func TestReviewTracker_Reject_TrackerBornIssue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)
	fakeT := withFakeTracker(t, rig)
	seedBranchRef(t, rig, "77", "77@feat@my-feature", "fake")
	bringToInReview(t, ctx, rig, fakeT)

	p := &scriptedReviewPrompter{
		Branch:        &store.BranchRow{IssueSlug: "77", BranchName: "77@review"},
		TrackerStatus: "In Progress",
	}
	if err := runReviewRejectInteractive(ctx, rig.deps(), p); err != nil {
		t.Fatalf("runReviewRejectInteractive: %v", err)
	}

	t.Run("records the picked tracker status", func(t *testing.T) {
		assertOneUpdate(t, fakeT)
	})
}

func TestReviewTracker_ManualIssue_NoUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)
	fakeT := withFakeTracker(t, rig)
	// Manual ref: TrackerType is empty, so no tracker prompt should fire.
	seedBranchRef(t, rig, "77", "77@feat@my-feature", "")

	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout feature branch: %v", err)
	}
	picked := inProgressBranchRow(t, rig, "77")
	p := &scriptedReviewPrompter{Branch: picked, TrackerStatus: "In Progress"}

	if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
		t.Fatalf("runReviewRequestInteractive: %v", err)
	}

	t.Run("no tracker update recorded", func(t *testing.T) {
		if got := len(fakeT.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates len = %d, want 0; got %+v", got, fakeT.RecordedUpdates)
		}
	})
}

func TestReviewTracker_NoTrackerConfigured_NoUpdateNoPanic(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)
	// No withFakeTracker: rig.tracker stays nil (deps.tracker == nil).
	// A tracker-born ref is present, but the nil tracker must short-circuit.
	seedBranchRef(t, rig, "77", "77@feat@my-feature", "fake")

	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout feature branch: %v", err)
	}
	picked := inProgressBranchRow(t, rig, "77")
	p := &scriptedReviewPrompter{Branch: picked, TrackerStatus: "In Progress"}

	t.Run("request succeeds without a tracker", func(t *testing.T) {
		if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewRequestInteractive: %v", err)
		}
	})
}

func TestReviewTracker_AbsentBranchRef_NoUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)
	fakeT := withFakeTracker(t, rig)
	// No seedBranchRef: ReadBranchRef returns (nil, nil) → no prompt.

	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout feature branch: %v", err)
	}
	picked := inProgressBranchRow(t, rig, "77")
	p := &scriptedReviewPrompter{Branch: picked, TrackerStatus: "In Progress"}

	if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
		t.Fatalf("runReviewRequestInteractive: %v", err)
	}

	t.Run("no tracker update recorded", func(t *testing.T) {
		if got := len(fakeT.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates len = %d, want 0; got %+v", got, fakeT.RecordedUpdates)
		}
	})
}
