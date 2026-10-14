package issue

import (
	"errors"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// issue start <id> on a repo issue: a prefix resolves it, the record's type
// names the branch and no picker opens.
func TestStartID_RepoIssueByPrefix(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	flags := issuepkg.IssueStartFlags{Tracker: true}
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Open one", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	prompter := &scriptedStartPrompter{ConfirmBranch: true}

	runErr := issueflow.StartID(ctx, rig.deps(flags), prompter, rec.ShortID())

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartID: %v", runErr)
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

// With the mirror on an id the repo knows is started without a reconcile:
// a tracker issue not yet imported stays that way.
func TestStartID_MirrorOnKnownIDSkipsTheReconcile(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	rig.cfg.IssueTracker.Mirror = true
	rig.cfg.IssueTracker.Projects = []config.TrackerProject{{Name: "zf", ID: "piprim/git-zf"}}
	rig.tracker.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "open")}
	deps := rig.deps(issuepkg.IssueStartFlags{Tracker: true})
	deps.Mirror = issueflow.MirrorOf(rig.cfg, rig.tracker)
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Known", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	runErr := issueflow.StartID(ctx, deps, &scriptedStartPrompter{ConfirmBranch: true}, rec.ShortID())

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartID: %v", runErr)
		}
	})
	t.Run("the branch is created", func(t *testing.T) {
		exists, err := rig.client.BranchExists(rec.ShortID() + "@fix@known")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("the tracker was not reconciled", func(t *testing.T) {
		if _, err := issuepkg.Resolve(ctx, rig.client, "42"); !errors.Is(err, git.ErrIssueNotFound) {
			t.Errorf("Resolve 42: err = %v, want not found", err)
		}
	})
}

// With the mirror on a number the repo does not know is reconciled with the
// tracker: it is imported, then started.
func TestStartID_MirrorOnImportsTheTrackerNumber(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	rig.cfg.IssueTracker.Mirror = true
	rig.cfg.IssueTracker.Projects = []config.TrackerProject{{Name: "zf", ID: "piprim/git-zf"}}
	rig.tracker.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "open")}
	deps := rig.deps(issuepkg.IssueStartFlags{Tracker: true})
	deps.Mirror = issueflow.MirrorOf(rig.cfg, rig.tracker)

	prompter := &scriptedStartPrompter{IssueFromTracker: &issuepkg.Issue{Type: "feat"}, ConfirmBranch: true}

	runErr := issueflow.StartID(ctx, deps, prompter, "42")

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartID: %v", runErr)
		}
	})
	t.Run("the tracker issue was imported", func(t *testing.T) {
		rec, err := issuepkg.Resolve(ctx, rig.client, "42")
		if err != nil || rec.Tracker == nil || rec.Tracker.ID != "42" {
			t.Errorf("rec = %+v, %v", rec, err)
		}
	})
	t.Run("the branch is named after the tracker number", func(t *testing.T) {
		exists, err := rig.client.BranchExists("42@feat@from-tracker")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
}

// With the mirror off a number the repo does not know is asked to the
// tracker and started as a tracker issue, the type form included.
func TestStartID_MirrorOffAsksTheTracker(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	flags := issuepkg.IssueStartFlags{Tracker: true}
	rig.tracker.ProjectIssues = []tracker.Issue{{ID: "ABC-3", Subject: "Implement OAuth", TrackerType: "fake"}}

	prompter := &scriptedStartPrompter{
		IssueFromTracker: &issuepkg.Issue{Type: "feat"},
		ConfirmBranch:    true,
		TrackerStatus:    "In Progress",
	}

	runErr := issueflow.StartID(ctx, rig.deps(flags), prompter, "ABC-3")

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartID: %v", runErr)
		}
	})
	t.Run("the branch is named after the tracker issue and the picked type", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-3@feat@implement-oauth")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("the branch chain records the tracker origin", func(t *testing.T) {
		ref, err := branch.Load(ctx, rig.client, "ABC-3")
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

// An id nothing knows opens the manual form with that id filled in: the
// branch starts as a manual one, for a tracker git-zf does not talk to.
func TestStartID_UnknownIDOpensTheManualForm(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Issue: tracker.Issue{ID: "JIRA-42", Subject: "Foreign issue"}, Type: "feat"},
		ConfirmBranch: true,
	}

	runErr := issueflow.StartID(ctx, rig.deps(issuepkg.IssueStartFlags{Tracker: true}), prompter, "JIRA-42")

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartID: %v", runErr)
		}
	})
	t.Run("the form was offered the id", func(t *testing.T) {
		if prompter.OfferedID != "JIRA-42" {
			t.Errorf("offered id = %q", prompter.OfferedID)
		}
	})
	t.Run("the branch is named after the typed id", func(t *testing.T) {
		exists, err := rig.client.BranchExists("JIRA-42@feat@foreign-issue")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("the branch is a manual one", func(t *testing.T) {
		ref, err := branch.Load(ctx, rig.client, "JIRA-42")
		if err != nil || ref == nil || ref.TrackerType != "" || ref.IssueID != "" {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
	t.Run("no repo issue was filed", func(t *testing.T) {
		if records, _, err := issuepkg.List(ctx, rig.client); err != nil || len(records) != 0 {
			t.Errorf("records = %+v, %v", records, err)
		}
	})
}

// Esc in that form starts nothing.
func TestStartID_UnknownIDFormAbortedStartsNothing(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	prompter := &scriptedStartPrompter{ConfirmBranch: true}

	runErr := issueflow.StartID(t.Context(), rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter, "nope-1")

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartID: %v", runErr)
		}
	})
	t.Run("no branch was created", func(t *testing.T) {
		if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
			t.Errorf("branches = %q, want only main", out)
		}
	})
}

// issue start without an id is the list: with no terminal the table is
// printed, as `git zf issue` does.
func TestStartCmd_NoIDListsTheIssues(t *testing.T) {
	rig := newRecordRig(t, "alice", "")
	rec, err := issuepkg.Create(t.Context(), rig.client, issuepkg.NewIssue{Title: "Default list", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Chdir(rig.dir)

	root := New(rig.cfg).GetRootCmd()
	root.SetArgs([]string{"start"})
	root.SetOut(rig.stdout)
	root.SetErr(rig.stderr)
	execErr := root.ExecuteContext(t.Context())

	t.Run("the command succeeds", func(t *testing.T) {
		if execErr != nil {
			t.Fatalf("Execute: %v", execErr)
		}
	})
	t.Run("the table lists the issue", func(t *testing.T) {
		if out := rig.stdout.String(); !strings.Contains(out, rec.ShortID()) || !strings.Contains(out, "Default list") {
			t.Errorf("stdout = %q", out)
		}
	})
}

// The list run by issue start carries its flags to the start behind `s`.
func TestListInfraDeps_CarriesTheStartFlags(t *testing.T) {
	t.Parallel()

	infra := issueListInfra{startFlags: issuepkg.IssueStartFlags{Variant: "spike", ParentIssueSlug: "parent-1"}}
	deps := infra.deps()

	t.Run("the tracker is listed", func(t *testing.T) {
		if !deps.Flags.Tracker {
			t.Error("Tracker = false")
		}
	})
	t.Run("the variant and the parent are kept", func(t *testing.T) {
		if deps.Flags.Variant != "spike" || deps.Flags.ParentIssueSlug != "parent-1" {
			t.Errorf("flags = %+v", deps.Flags)
		}
	})
}
