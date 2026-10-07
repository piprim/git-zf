package issue

import (
	"slices"
	"testing"
	"time"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// mirrorOn turns the mirror on for a close rig and returns it.
func mirrorOn(rig *closeTestRig) *issuepkg.Mirror {
	rig.cfg.IssueTracker = config.IssueTrackerConfig{
		Type: "fake", Mirror: true,
		Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "piprim/git-zf"}},
	}

	return mirrorOf(rig.cfg, rig.tracker)
}

// closeTrackerBorn closes the rig's branch ABC-1, born from tracker issue
// ABC-1 and mirrored, picking status in the status picker.
func closeTrackerBorn(t *testing.T, status string) (*closeTestRig, issuepkg.Record, error) {
	t.Helper()

	rig := newCloseRig(t)
	ctx := t.Context()
	m := mirrorOn(rig)
	rig.tracker.ClosingStatuses = []string{"Closed"}
	rig.tracker.ProjectIssues = []tracker.Issue{{
		TrackerType: "fake", ID: "ABC-1", Subject: "Add thing", Status: "New",
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}}
	if _, err := m.Reconcile(ctx, rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rec, err := issuepkg.Resolve(ctx, rig.client, importedID(t, rig))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	branchtest.Amend(t, rig.client, branch.Op{Branch: "ABC-1@feat@add-thing", TrackerType: "fake", IssueID: rec.ID})

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategySquash,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: status,
		DeleteBranch:  true,
	}
	runErr := runClose(ctx, rig.deps(), prompter)

	got, err := issuepkg.Load(ctx, rig.client, rec.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	return rig, got, runErr
}

// importedID returns the full ID of the rig's only issue.
func importedID(t *testing.T, rig *closeTestRig) string {
	t.Helper()

	records, _, err := issuepkg.List(t.Context(), rig.client)
	if err != nil || len(records) != 1 {
		t.Fatalf("want 1 issue, got %d (%v)", len(records), err)
	}

	return records[0].ID
}

func TestClose_Mirror_TrackerBornFollowsTheTracker(t *testing.T) {
	t.Parallel()

	rig, rec, runErr := closeTrackerBorn(t, "Closed")

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("the picked status is applied to the tracker", func(t *testing.T) {
		want := []fake.Update{{IssueID: "ABC-1", StatusName: "Closed"}}
		if !slices.Equal(rig.tracker.RecordedUpdates, want) {
			t.Errorf("updates = %+v", rig.tracker.RecordedUpdates)
		}
	})
	t.Run("the record follows the tracker and is closed", func(t *testing.T) {
		if rec.State != issuepkg.StateClosed || rec.TrackerState != issuepkg.StateClosed {
			t.Errorf("State = %q, TrackerState = %q", rec.State, rec.TrackerState)
		}
	})
	t.Run("the mirror did not close the tracker issue itself", func(t *testing.T) {
		if len(rig.tracker.RecordedOpens) != 0 {
			t.Errorf("tracker writes = %+v", rig.tracker.RecordedOpens)
		}
	})
}

// On Redmine, "Resolved" is not a closed status: the mirror must not force a
// close over the status the operator picked.
func TestClose_Mirror_TrackerBornKeepsAnOpenStatus(t *testing.T) {
	t.Parallel()

	rig, rec, runErr := closeTrackerBorn(t, "In Progress")

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("the record stays open with the picked status name", func(t *testing.T) {
		if rec.State != issuepkg.StateOpen || rec.TrackerStatus != "In Progress" {
			t.Errorf("State = %q, TrackerStatus = %q", rec.State, rec.TrackerStatus)
		}
	})
	t.Run("the tracker issue is not closed", func(t *testing.T) {
		closed, _ := rig.tracker.IsIssueClosed(t.Context(), "ABC-1")
		if closed || len(rig.tracker.RecordedOpens) != 0 {
			t.Errorf("closed = %v, tracker writes = %+v", closed, rig.tracker.RecordedOpens)
		}
	})
}

func TestClose_Mirror_RepoBornClosesTheTrackerIssue(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	ctx := t.Context()
	m := mirrorOn(rig)

	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Add thing", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.Reconcile(ctx, rig.client); err != nil { // exports it as tracker issue 1
		t.Fatalf("Reconcile: %v", err)
	}
	branchtest.Amend(t, rig.client, branch.Op{Branch: "ABC-1@feat@add-thing", IssueID: rec.ID})

	prompter := &scriptedPrompter{
		Branch:       rig.pickedBranchRow(),
		Strategy:     commitpkg.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("feat(thing): close ABC-1\n"),
		DeleteBranch: true,
	}
	runErr := runClose(ctx, rig.deps(), prompter)
	got, _ := issuepkg.Load(ctx, rig.client, rec.ID)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("the record is closed", func(t *testing.T) {
		if got.State != issuepkg.StateClosed {
			t.Errorf("State = %q", got.State)
		}
	})
	t.Run("the tracker issue is closed by the mirror", func(t *testing.T) {
		if !slices.Equal(rig.tracker.RecordedOpens, []fake.Open{{IssueID: "1", Open: false}}) {
			t.Errorf("tracker writes = %+v", rig.tracker.RecordedOpens)
		}
	})
	t.Run("no status picker ran", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 0 {
			t.Errorf("updates = %+v", rig.tracker.RecordedUpdates)
		}
	})
}
