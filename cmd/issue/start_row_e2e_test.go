package issue

import (
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/cmd/issueflow"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// rowOf lists the rows of the rig and returns the one with slug.
func rowOf(t *testing.T, rig *startTestRig, flags issuepkg.IssueStartFlags, slug string) *issuepkg.Row {
	t.Helper()

	rows, err := issueflow.ListRows(t.Context(), rig.deps(flags), "")
	if err != nil {
		t.Fatalf("ListRows: %v", err)
	}
	for i := range rows {
		if rows[i].IssueSlug == slug {
			return &rows[i]
		}
	}
	t.Fatalf("no row %q among %+v", slug, rows)

	return nil
}

// s in issue list on a tracker issue: the type form, then a tracker branch.
func TestStartRow_TrackerRow(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{Tracker: true}
	rig.tracker.ProjectIssues = []tracker.Issue{{ID: "ABC-3", Subject: "Implement OAuth", TrackerType: "fake"}}
	row := rowOf(t, rig, flags, "ABC-3")

	prompter := &scriptedStartPrompter{
		IssueFromTracker: &issuepkg.Issue{Type: "feat"},
		ConfirmBranch:    true,
		TrackerStatus:    "In Progress",
	}

	err := issueflow.StartRow(t.Context(), rig.deps(flags), prompter, row)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("StartRow: %v", err)
		}
	})
	t.Run("the branch is named after the tracker issue and the picked type", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-3@feat@implement-oauth")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("the branch chain records the tracker origin", func(t *testing.T) {
		ref, err := branch.Load(t.Context(), rig.client, "ABC-3")
		if err != nil || ref == nil || ref.TrackerType != "fake" {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
	t.Run("the tracker status was updated", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 1 || rig.tracker.RecordedUpdates[0].IssueID != "ABC-3" {
			t.Errorf("updates = %+v", rig.tracker.RecordedUpdates)
		}
	})
}

// s on a repo issue: the record's type names the branch, no form asked.
func TestStartRow_RepoRow(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	flags := issuepkg.IssueStartFlags{Tracker: true}
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Open one", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	row := rowOf(t, rig, flags, rec.ShortID())

	prompter := &scriptedStartPrompter{ConfirmBranch: true}

	runErr := issueflow.StartRow(ctx, rig.deps(flags), prompter, row)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartRow: %v", runErr)
		}
	})
	t.Run("the branch is created with the record's type", func(t *testing.T) {
		exists, err := rig.client.BranchExists(rec.ShortID() + "@fix@open-one")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("the branch ref records the full issue ID", func(t *testing.T) {
		ref, err := branch.Load(ctx, rig.client, rec.ShortID())
		if err != nil || ref == nil || ref.IssueID != rec.ID {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
	t.Run("the table was not opened", func(t *testing.T) {
		if prompter.OfferedRows != nil {
			t.Errorf("offered = %+v", prompter.OfferedRows)
		}
	})
}

// Review Focus 1. A branch started under another slug is joined to its
// record by the chain's issue ID: starting that row resolves the record by
// that ID, and the conflict loop sees the existing name.
func TestStartRow_RowJoinedByIssueID(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	flags := issuepkg.IssueStartFlags{Tracker: true}
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Renamed", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	branchtest.Seed(t, rig.client,
		branch.Op{Branch: "other-slug@fix@renamed", Title: "Renamed", IssueID: rec.ID}, branch.StatusInProgress)
	row := rowOf(t, rig, flags, "other-slug")

	prompter := &scriptedStartPrompter{ConfirmBranch: true, ConflictAbort: true}

	runErr := issueflow.StartRow(ctx, rig.deps(flags), prompter, row)

	t.Run("the record is found by the chain's issue ID: no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartRow: %v", runErr)
		}
	})
	t.Run("the scripted abort creates nothing", func(t *testing.T) {
		if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
			t.Errorf("branches = %q, want only main", out)
		}
	})
}

// Review finding. A branch started with a typed ID (no record, not listed by
// the tracker) is a manual branch: picking its row in branch new, with a
// tracker configured, must neither ask the tracker nor record a tracker
// origin on the new chain entry.
func TestRunIssueStart_BranchNewPicksAManualBranchRow(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	flags := issuepkg.IssueStartFlags{Tracker: false}
	branchtest.Seed(t, rig.client, branch.Op{Branch: "ABC-5@feat@manual", Title: "Manual"}, branch.StatusInProgress)

	prompter := &scriptedStartPrompter{
		PickSlug:         "ABC-5",
		IssueFromTracker: &issuepkg.Issue{Type: "feat"},
		ConfirmBranch:    true,
		TrackerStatus:    "In Progress",
	}

	err := issueflow.RunIssueStart(ctx, rig.deps(flags), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})
	t.Run("the row was offered and picked", func(t *testing.T) {
		if len(prompter.OfferedRows) != 1 || prompter.OfferedRows[0].IssueSlug != "ABC-5" {
			t.Errorf("offered = %+v", prompter.OfferedRows)
		}
	})
	t.Run("the tracker was never asked", func(t *testing.T) {
		if rig.tracker.ListCalls != 0 || len(rig.tracker.RecordedUpdates) != 0 {
			t.Errorf("ListCalls = %d, updates = %+v", rig.tracker.ListCalls, rig.tracker.RecordedUpdates)
		}
	})
	t.Run("the chain keeps a manual origin", func(t *testing.T) {
		ref, err := branch.Load(ctx, rig.client, "ABC-5")
		if err != nil || ref == nil || ref.TrackerType != "" {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
}
